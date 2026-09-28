// Package wsauth attaches the workspace service token to requests the agent
// server makes to the workspace API. The workspace service requires the token
// on every /api route whenever WORKSPACE_API_TOKEN is configured; coding-CLI
// shells never receive it, so only this process can reach the service.
package wsauth

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// HeaderName is the header the workspace service checks.
const HeaderName = "X-Workspace-Token"

// Token is the configured workspace service token, or "".
func Token() string { return strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")) }

// SetHeader stamps the token on req when one is configured and the request
// does not already carry one.
func SetHeader(req *http.Request) {
	if req == nil || req.Header.Get(HeaderName) != "" {
		return
	}
	if token := Token(); token != "" {
		req.Header.Set(HeaderName, token)
	}
}

// workspaceHost is the host:port of WORKSPACE_API_URL, read per request so
// tests and late configuration are honoured.
func workspaceHost() string {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE_API_URL"))
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// IsWorkspaceRequest reports whether req targets the configured workspace API.
func IsWorkspaceRequest(req *http.Request) bool {
	host := workspaceHost()
	return host != "" && req != nil && req.URL != nil && strings.EqualFold(req.URL.Host, host)
}

type transport struct{ base http.RoundTripper }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if IsWorkspaceRequest(req) && req.Header.Get(HeaderName) == "" && Token() != "" {
		clone := req.Clone(req.Context())
		clone.Header.Set(HeaderName, Token())
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(req)
}

// Transport wraps base (http.DefaultTransport when nil) so requests to the
// workspace API carry the token. Requests to any other host pass untouched.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, already := base.(transport); already {
		return base
	}
	return transport{base: base}
}

var installOnce sync.Once

// InstallDefault makes requests on http.DefaultTransport (http.DefaultClient
// and every client built without its own Transport) carry the token.
//
// http.DefaultTransport must stay an *http.Transport: libraries assert that
// type (whatsmeow's NewClient does http.DefaultTransport.(*http.Transport)
// and panicked on a wrapper). So the token is attached through the
// transport's Proxy hook, which http.Transport calls with each outgoing
// request before its headers are written; the proxy decision itself is
// delegated unchanged.
func InstallDefault() {
	installOnce.Do(func() {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return
		}
		proxy := base.Proxy
		base.Proxy = func(req *http.Request) (*url.URL, error) {
			if IsWorkspaceRequest(req) && req.Header.Get(HeaderName) == "" {
				if token := Token(); token != "" {
					req.Header.Set(HeaderName, token)
				}
			}
			if proxy == nil {
				return nil, nil
			}
			return proxy(req)
		}
	})
}
