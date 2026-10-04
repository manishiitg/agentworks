// Package upstream speaks to one upstream MCP server over Streamable HTTP.
package upstream

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
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// Client is a bound upstream MCP session.
type Client struct {
	c   *client.Client
	url string
}

type DialOptions struct {
	// AllowPrivate is for an explicitly configured private-network deployment.
	AllowPrivate bool
	BearerToken  string
	AccessToken  func(context.Context) (string, error)
}

const maxResponseBytes = 2 << 20

// ValidateURL rejects credentials, redirects to non-MCP schemes, and cleartext
// destinations outside an explicitly permitted loopback development setup.
func ValidateURL(raw string, opts DialOptions) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid upstream URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !opts.AllowPrivate || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("upstream URL must use HTTPS (loopback HTTP requires private-network mode)")
		}
	}
	return u, nil
}

func privateIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var one [1]byte
		n, err := b.ReadCloser.Read(one[:])
		if n > 0 {
			return 0, errors.New("upstream response exceeds size limit")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

type boundedTransport struct {
	base        http.RoundTripper
	bearerToken string
	accessToken func(context.Context) (string, error)
}

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token := t.bearerToken
	if t.accessToken != nil {
		var err error
		token, err = t.accessToken(req.Context())
		if err != nil || token == "" {
			return nil, errors.New("shared OAuth authorization required; reconnect the server")
		}
	}
	if token != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > maxResponseBytes {
		resp.Body.Close()
		return nil, errors.New("upstream response exceeds size limit")
	}
	resp.Body = &limitedBody{ReadCloser: resp.Body, remaining: maxResponseBytes}
	return resp, nil
}

func safeHTTPClient(opts DialOptions) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	base := &http.Transport{
		Proxy:                 nil,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConnsPerHost:   4,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(resolved) == 0 {
				return nil, fmt.Errorf("upstream DNS lookup failed: %w", err)
			}
			for _, entry := range resolved {
				if !opts.AllowPrivate && privateIP(entry.IP) {
					return nil, errors.New("upstream resolves to a private or special-use address")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(resolved[0].IP.String(), port))
		},
	}
	return &http.Client{
		Transport:     boundedTransport{base: base, bearerToken: opts.BearerToken, accessToken: opts.AccessToken},
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Dial opens a Streamable HTTP session and runs MCP initialize.
func Dial(ctx context.Context, url string) (*Client, error) {
	return DialWithOptions(ctx, url, DialOptions{})
}

func DialWithOptions(ctx context.Context, url string, opts DialOptions) (*Client, error) {
	if _, err := ValidateURL(url, opts); err != nil {
		return nil, err
	}
	c, err := client.NewStreamableHttpClient(url, transport.WithHTTPBasicClient(safeHTTPClient(opts)))
	if err != nil {
		return nil, fmt.Errorf("upstream client: %w", err)
	}
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("upstream start: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcp-gateway", Version: "0.1.0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		c.Close()
		return nil, fmt.Errorf("upstream initialize: %w", err)
	}
	return &Client{c: c, url: url}, nil
}

func (u *Client) Close() error { return u.c.Close() }

func (u *Client) URL() string { return u.url }

// Discover returns every tool the upstream exposes (all pages).
func (u *Client) Discover(ctx context.Context) ([]mcp.Tool, error) {
	res, err := u.c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("upstream tools/list: %w", err)
	}
	return res.Tools, nil
}

// Call invokes one upstream tool by its upstream name.
func (u *Client) Call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := u.c.CallTool(ctx, req)
	if err != nil {
		return nil, err
	}
	return res, nil
}
