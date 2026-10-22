// Package upstream speaks to one upstream MCP server over Streamable HTTP.
package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// Client is a bound upstream MCP session.
type Client struct {
	c           *client.Client
	url         string
	diagnostics *refusalNote
}

type DialOptions struct {
	// AllowPrivate is for an explicitly configured private-network deployment.
	Transport    string
	AllowPrivate bool
	BearerToken  string
	AuthScheme   string
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
	authScheme  string
	accessToken func(context.Context) (string, error)
	refusal     *refusalNote
}

// refusalNote keeps what the upstream server said when it answered an error, so a failed connect can show it.
type refusalNote struct {
	mu          sync.Mutex
	text        string
	credentials []string
}

// Only credentials actually sent on this connection are excluded from errors.
// Successful tool replies and the original error classification are unchanged.
func (n *refusalNote) rememberCredential(value string) {
	if value == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, existing := range n.credentials {
		if existing == value {
			return
		}
	}
	n.credentials = append(n.credentials, value)
}

func (n *refusalNote) redact(text string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Replace longer values first when a header includes another credential.
	values := append([]string(nil), n.credentials...)
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	return text
}

type credentialSafeError struct {
	cause error
	text  string
}

func (e credentialSafeError) Error() string { return e.text }
func (e credentialSafeError) Unwrap() error { return e.cause }

func (n *refusalNote) safeError(err error) error {
	if err == nil {
		return nil
	}
	text := n.redact(err.Error())
	if text == err.Error() {
		return err
	}
	return credentialSafeError{cause: err, text: text}
}

func (n *refusalNote) set(text string) {
	n.mu.Lock()
	n.text = text
	n.mu.Unlock()
}

func (n *refusalNote) get() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.text
}

// refusalReason is the server's own sentence: a JSON-RPC error message, else the start of the body.
func refusalReason(body string, redact func(string) string) string {
	var rpc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &rpc) == nil && strings.TrimSpace(rpc.Error.Message) != "" {
		body = rpc.Error.Message
	}
	body = strings.Join(strings.Fields(redact(body)), " ")
	if len(body) > 300 {
		body = body[:300]
	}
	return body
}

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	scheme := t.authScheme
	if scheme == "" {
		scheme = "Bearer"
	}
	if scheme != "Bearer" && scheme != "Basic" && scheme != "Headers" {
		return nil, errors.New("unsupported authorization scheme")
	}
	token := t.bearerToken
	if t.accessToken != nil {
		scheme = "Bearer" // OAuth credentials always use Bearer.
		var err error
		token, err = t.accessToken(req.Context())
		if err != nil || token == "" {
			return nil, errors.New("shared OAuth authorization required; reconnect the server")
		}
	}
	if token != "" {
		req = req.Clone(req.Context())
		if scheme == "Headers" {
			if err := ValidateCredentialHeaders(token); err != nil {
				return nil, err
			}
			var headers map[string]string
			_ = json.Unmarshal([]byte(token), &headers)
			for name, value := range headers {
				req.Header.Set(name, value)
				if t.refusal != nil {
					t.refusal.rememberCredential(value)
					if strings.EqualFold(name, "Authorization") {
						if _, credential, found := strings.Cut(value, " "); found {
							t.refusal.rememberCredential(strings.TrimSpace(credential))
						}
					}
				}
			}
		} else {
			req.Header.Set("Authorization", scheme+" "+token)
			if t.refusal != nil {
				t.refusal.rememberCredential(token)
			}
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if t.refusal != nil && resp.StatusCode >= 400 && resp.StatusCode != http.StatusUnauthorized && resp.Body != nil {
		// Decode/redact before shortening the diagnostic, so truncation cannot
		// expose a prefix of a credential at the old excerpt boundary.
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, t.refusal.safeError(readErr)
		}
		if len(body) > maxResponseBytes {
			return nil, errors.New("upstream response exceeds size limit")
		}
		resp.Body = io.NopCloser(strings.NewReader(string(body)))
		t.refusal.set(fmt.Sprintf("%s (HTTP %d)", refusalReason(string(body), t.refusal.redact), resp.StatusCode))
	}
	if resp.ContentLength > maxResponseBytes {
		resp.Body.Close()
		return nil, errors.New("upstream response exceeds size limit")
	}
	resp.Body = &limitedBody{ReadCloser: resp.Body, remaining: maxResponseBytes}
	return resp, nil
}

func safeHTTPClient(opts DialOptions) *http.Client { return safeHTTPClientNoting(opts, nil) }

// safeHTTPClientNoting is safeHTTPClient that also keeps the upstream's error answers in refusal.
func safeHTTPClientNoting(opts DialOptions, refusal *refusalNote) *http.Client {
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
		Transport:     boundedTransport{base: base, bearerToken: opts.BearerToken, authScheme: opts.AuthScheme, accessToken: opts.AccessToken, refusal: refusal},
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Dial opens a Streamable HTTP session and runs MCP initialize.
func Dial(ctx context.Context, url string) (*Client, error) {
	return DialWithOptions(ctx, url, DialOptions{})
}

func DialWithOptions(ctx context.Context, url string, opts DialOptions) (_ *Client, dialErr error) {
	if _, err := ValidateURL(url, opts); err != nil {
		return nil, err
	}
	refusal := &refusalNote{}
	defer func() { dialErr = refusal.safeError(dialErr) }()
	var c *client.Client
	var err error
	httpClient := safeHTTPClientNoting(opts, refusal)
	if opts.Transport == "sse" {
		c, err = client.NewSSEMCPClient(url, client.WithHTTPClient(httpClient))
	} else {
		c, err = client.NewStreamableHttpClient(url, transport.WithHTTPBasicClient(httpClient))
	}
	if err != nil {
		return nil, fmt.Errorf("upstream client: %w", err)
	}
	if err := c.Start(ctx); err != nil {
		if said := refusal.get(); said != "" && !IsAuthorizationRequired(err) {
			return nil, fmt.Errorf("upstream refused the connection: %s", said)
		}
		return nil, fmt.Errorf("upstream start: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcp-gateway", Version: "0.1.0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		c.Close()
		if said := refusal.get(); said != "" && !IsAuthorizationRequired(err) {
			return nil, fmt.Errorf("upstream refused the connection: %s", said)
		}
		return nil, fmt.Errorf("upstream initialize: %w", err)
	}
	return &Client{c: c, url: url, diagnostics: refusal}, nil
}

func (u *Client) Close() error { return u.diagnostics.safeError(u.c.Close()) }

// IsAuthorizationRequired reports an upstream that answered 401 because it needs a sign-in.
func IsAuthorizationRequired(err error) bool {
	return errors.Is(err, transport.ErrAuthorizationRequired) || errors.Is(err, transport.ErrOAuthAuthorizationRequired)
}

func (u *Client) URL() string { return u.url }

// Discover returns every tool the upstream exposes (all pages).
func (u *Client) Discover(ctx context.Context) ([]mcp.Tool, error) {
	res, err := u.c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, u.diagnostics.safeError(fmt.Errorf("upstream tools/list: %w", err))
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
		return nil, u.diagnostics.safeError(err)
	}
	return res, nil
}

// Credential header values are kept in the existing encrypted credential store.
func ValidateCredentialHeaders(raw string) error {
	if len(raw) > 65536 {
		return errors.New("credential headers too large")
	}
	var headers map[string]string
	if json.Unmarshal([]byte(raw), &headers) != nil || len(headers) == 0 || len(headers) > 32 {
		return errors.New("invalid credential headers")
	}
	for name, value := range headers {
		if name == "" || strings.ContainsAny(value, "\r\n") {
			return errors.New("invalid credential header")
		}
		for _, r := range name {
			if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
				return errors.New("invalid credential header name")
			}
		}
		switch strings.ToLower(name) {
		case "host", "connection", "content-length", "transfer-encoding", "proxy-authorization":
			return errors.New("unsupported credential header")
		}
	}
	return nil
}
