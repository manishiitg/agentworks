// Package netguard builds HTTP clients that only reach the public internet.
//
// A server whose URL a user supplies (a personal MCP server, its OAuth
// endpoints) must never let the platform reach its own services, the host's
// private network or cloud metadata (SSRF). The check runs on the address
// actually being dialled, so DNS rebinding between a check and the connect
// cannot get past it; proxies are disabled, and redirects may not leave the
// origin (Go forwards custom headers such as X-API-Key across hosts).
package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is returned for a destination that is not public.
var ErrBlocked = errors.New("destination is not a public internet address")

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, cidr := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
		"fd00:ec2::254/128",
	} {
		out = append(out, netip.MustParsePrefix(cidr))
	}
	return out
}()

var blockedHostnames = map[string]bool{
	"localhost": true, "metadata.google.internal": true, "metadata": true, "instance-data": true,
}

// PublicAddr reports whether ip is a public unicast address.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap() // IPv4-mapped IPv6 is checked as IPv4
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// CheckURL validates a user-supplied URL before any request: http(s) only,
// a host that is not a literal private address or a known internal name.
// requireHTTPS additionally refuses plain http (browser-facing auth URLs).
func CheckURL(raw string, requireHTTPS bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if requireHTTPS {
			return fmt.Errorf("%w: https is required", ErrBlocked)
		}
	default:
		return fmt.Errorf("%w: scheme %q is not allowed", ErrBlocked, u.Scheme)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrBlocked)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in the URL are not allowed", ErrBlocked)
	}
	if blockedHostnames[host] || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("%w: %s", ErrBlocked, host)
	}
	if ip, err := netip.ParseAddr(host); err == nil && !PublicAddr(ip) {
		return fmt.Errorf("%w: %s", ErrBlocked, host)
	}
	return nil
}

// Option adjusts a client or transport. The zero configuration is the
// strict one: public addresses only, same-origin redirects, no body cap.
type Option func(*options)

type options struct {
	allowPrivate     bool
	maxResponseBytes int64
	noRedirects      bool
}

// AllowPrivate permits private and loopback destinations (a local
// development setup). Proxies stay disabled and URL schemes are still checked.
func AllowPrivate() Option { return func(o *options) { o.allowPrivate = true } }

// MaxResponseBytes fails a response body read past n bytes.
func MaxResponseBytes(n int64) Option { return func(o *options) { o.maxResponseBytes = n } }

// NoRedirects refuses every redirect, not only cross-origin ones.
func NoRedirects() Option { return func(o *options) { o.noRedirects = true } }

func collect(opts []Option) options {
	var o options
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// control refuses any socket to a non-public address. It runs for every
// dial attempt with the resolved address, after DNS.
func control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlocked, address)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !PublicAddr(ip) {
		return fmt.Errorf("%w: %s", ErrBlocked, host)
	}
	return nil
}

// Transport is an http.Transport that dials public addresses only (unless
// AllowPrivate) and never uses a proxy.
func Transport(opts ...Option) *http.Transport {
	o := collect(opts)
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: control}
	if o.allowPrivate {
		dialer.Control = nil
	}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// Client returns a public-only client. timeout 0 means no overall timeout
// (streaming transports hold responses open).
func Client(timeout time.Duration, opts ...Option) *http.Client {
	o := collect(opts)
	var transport http.RoundTripper = Transport(opts...)
	if o.maxResponseBytes > 0 {
		transport = limitedTransport{next: transport, limit: o.maxResponseBytes}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if o.noRedirects {
				return fmt.Errorf("%w: redirects are not followed (%s)", ErrBlocked, req.URL.Host)
			}
			if len(via) >= 5 {
				return fmt.Errorf("%w: too many redirects", ErrBlocked)
			}
			first := via[0].URL
			if req.URL.Scheme != first.Scheme || !strings.EqualFold(req.URL.Host, first.Host) {
				return fmt.Errorf("%w: redirect to another origin (%s)", ErrBlocked, req.URL.Host)
			}
			if o.allowPrivate {
				return nil
			}
			return CheckURL(req.URL.String(), false)
		},
	}
}

// ErrResponseTooLarge is returned when a body passes MaxResponseBytes.
var ErrResponseTooLarge = errors.New("response body exceeds the size limit")

type limitedTransport struct {
	next  http.RoundTripper
	limit int64
}

func (t limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &limitedBody{body: resp.Body, remaining: t.limit}
	return resp, nil
}

type limitedBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		// One byte past the limit decides between EOF and too large.
		var probe [1]byte
		if n, _ := b.body.Read(probe[:]); n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.body.Close() }

// Context attaches a public-only client for libraries (golang.org/x/oauth2)
// that take their HTTP client from the context.
type contextKey struct{}

// WithClient returns ctx carrying client; FromContext retrieves it.
func WithClient(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, contextKey{}, client)
}

// FromContext returns the client stored by WithClient, or nil.
func FromContext(ctx context.Context) *http.Client {
	client, _ := ctx.Value(contextKey{}).(*http.Client)
	return client
}
