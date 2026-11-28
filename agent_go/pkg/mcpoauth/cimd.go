package mcpoauth

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
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxCIMDURLBytes     = 1024
	maxCIMDBodyBytes    = 16 * 1024
	maxCIMDRedirectURIs = 20
	maxCIMDCacheEntries = 512
	defaultCIMDCacheTTL = 5 * time.Minute
	maxCIMDCacheTTL     = time.Hour
)

var cimdBlockedPrefixes = func() []netip.Prefix {
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

var cimdBlockedHostnames = map[string]bool{
	"localhost":                true,
	"metadata.google.internal": true,
	"metadata":                 true,
	"instance-data":            true,
}

func isPublicCIMDIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, prefix := range cimdBlockedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func cimdDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("client metadata destination blocked: %s", address)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !isPublicCIMDIP(ip) {
		return fmt.Errorf("client metadata destination is not public: %s", host)
	}
	return nil
}

func newSafeCIMDHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   cimdDialControl,
	}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          16,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not allowed for client metadata documents")
		},
	}
}

// IsClientIDMetadataURL reports whether raw looks like an HTTPS URL client_id
// subject to OAuth Client ID Metadata Documents (SEP-991).
func IsClientIDMetadataURL(raw string) bool {
	return strings.HasPrefix(raw, "https://")
}

func validateCIMDURL(raw string, allowPrivate bool) (*url.URL, error) {
	if raw == "" || len(raw) > maxCIMDURLBytes || strings.TrimSpace(raw) != raw {
		return nil, errors.New("invalid client metadata URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid client metadata URL")
	}
	// SEP-991 / draft-ietf-oauth-client-id-metadata-document requires a non-root
	// path component and forbids dot-segments.
	if u.Path == "" || u.Path == "/" || strings.Contains(raw, "\\") {
		return nil, errors.New("client metadata URL must include a path")
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		lower := strings.ToLower(seg)
		if lower == "." || lower == ".." || lower == "%2e" || lower == "%2e%2e" || lower == ".%2e" || lower == "%2e." {
			return nil, errors.New("client metadata URL must not contain dot segments")
		}
	}
	if !allowPrivate {
		host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		if host == "" || cimdBlockedHostnames[host] || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
			return nil, errors.New("client metadata host is not public")
		}
		if ip, err := netip.ParseAddr(host); err == nil && !isPublicCIMDIP(ip) {
			return nil, errors.New("client metadata IP is not public")
		}
	}
	return u, nil
}

type cimdCacheEntry struct {
	client    Client
	expiresAt time.Time
}

// CIMDResolver fetches, validates, and caches OAuth Client ID Metadata
// Documents (SEP-991) for URL-based client_ids.
type CIMDResolver struct {
	client       *http.Client
	allowPrivate bool
	now          func() time.Time

	mu    sync.Mutex
	cache map[string]cimdCacheEntry
}

// DefaultCIMDResolver enforces strict public-internet HTTPS fetching with
// dial-time SSRF checks, redirect refusal, size limits, and bounded caching.
var DefaultCIMDResolver = NewCIMDResolver()

// NewCIMDResolver returns a production CIMD resolver that only dials public
// internet addresses.
func NewCIMDResolver() *CIMDResolver {
	return &CIMDResolver{
		client: newSafeCIMDHTTPClient(),
		now:    time.Now,
		cache:  make(map[string]cimdCacheEntry),
	}
}

// NewCIMDResolverWithClient constructs a resolver backed by a custom HTTP
// client (for tests against loopback TLS servers). When allowPrivate is false,
// public-only URL validation still applies. Redirects are always refused.
func NewCIMDResolverWithClient(client *http.Client, allowPrivate bool) *CIMDResolver {
	if client == nil {
		client = newSafeCIMDHTTPClient()
	} else {
		copied := *client
		copied.CheckRedirect = func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not allowed for client metadata documents")
		}
		client = &copied
	}
	return &CIMDResolver{
		client:       client,
		allowPrivate: allowPrivate,
		now:          time.Now,
		cache:        make(map[string]cimdCacheEntry),
	}
}

type cimdDocument struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// Resolve fetches (or returns from cache) and validates the metadata document
// for an HTTPS URL client_id.
func (r *CIMDResolver) Resolve(ctx context.Context, clientID string) (Client, error) {
	u, err := validateCIMDURL(clientID, r.allowPrivate)
	if err != nil {
		return Client{}, err
	}
	now := r.now()
	if cached, ok := r.getCached(clientID, now); ok {
		return cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return Client{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AgentWorks-OAuth-CIMD/1.0")
	resp, err := r.client.Do(req)
	if err != nil {
		return Client{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Client{}, fmt.Errorf("client metadata fetch returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCIMDBodyBytes+1))
	if err != nil {
		return Client{}, err
	}
	if len(body) > maxCIMDBodyBytes {
		return Client{}, errors.New("client metadata document too large")
	}
	var doc cimdDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return Client{}, errors.New("invalid client metadata JSON")
	}
	if doc.ClientID != clientID {
		return Client{}, errors.New("client metadata client_id mismatch")
	}
	if doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != "none" {
		return Client{}, errors.New("unsupported client metadata token_endpoint_auth_method")
	}
	if len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > maxCIMDRedirectURIs {
		return Client{}, errors.New("invalid client metadata redirect_uris")
	}
	for _, uri := range doc.RedirectURIs {
		if !ValidRedirect(uri) {
			return Client{}, errors.New("invalid client metadata redirect_uri")
		}
	}
	if len(doc.GrantTypes) > 0 && (!slices.Contains(doc.GrantTypes, "authorization_code") || len(doc.GrantTypes) > 2 || len(doc.GrantTypes) == 2 && !slices.Contains(doc.GrantTypes, "refresh_token")) {
		return Client{}, errors.New("invalid client metadata grant_types")
	}
	if len(doc.ResponseTypes) > 0 && (len(doc.ResponseTypes) != 1 || doc.ResponseTypes[0] != "code") {
		return Client{}, errors.New("invalid client metadata response_types")
	}
	name := strings.TrimSpace(doc.ClientName)
	if name == "" {
		name = u.Hostname()
	}
	if len(name) > 128 {
		return Client{}, errors.New("client metadata client_name too long")
	}
	client := Client{
		ID:           clientID,
		Name:         name,
		RedirectURIs: slices.Clone(doc.RedirectURIs),
	}
	if ttl := parseCIMDCacheTTL(resp.Header.Get("Cache-Control")); ttl > 0 {
		r.putCached(clientID, client, now.Add(ttl), now)
	}
	return client, nil
}

func parseCIMDCacheTTL(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return defaultCIMDCacheTTL
	}
	hasMaxAge := false
	var maxAge time.Duration
	for _, rawPart := range strings.Split(header, ",") {
		part := strings.ToLower(strings.TrimSpace(rawPart))
		switch {
		case part == "no-store" || part == "no-cache":
			return 0
		case strings.HasPrefix(part, "max-age="):
			val := strings.Trim(strings.TrimPrefix(part, "max-age="), `"`)
			secs, err := strconv.Atoi(val)
			if err != nil || secs <= 0 {
				return 0
			}
			hasMaxAge = true
			maxAge = time.Duration(secs) * time.Second
		}
	}
	if !hasMaxAge {
		return defaultCIMDCacheTTL
	}
	if maxAge > maxCIMDCacheTTL {
		return maxCIMDCacheTTL
	}
	return maxAge
}

func (r *CIMDResolver) getCached(clientID string, now time.Time) (Client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[clientID]
	if !ok {
		return Client{}, false
	}
	if !entry.expiresAt.After(now) {
		delete(r.cache, clientID)
		return Client{}, false
	}
	return Client{
		ID:           entry.client.ID,
		Name:         entry.client.Name,
		RedirectURIs: slices.Clone(entry.client.RedirectURIs),
	}, true
}

func (r *CIMDResolver) putCached(clientID string, client Client, expiresAt, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]cimdCacheEntry)
	}
	if _, exists := r.cache[clientID]; !exists && len(r.cache) >= maxCIMDCacheEntries {
		for k, v := range r.cache {
			if !v.expiresAt.After(now) {
				delete(r.cache, k)
			}
		}
		if len(r.cache) >= maxCIMDCacheEntries {
			var oldestKey string
			var oldestExp time.Time
			first := true
			for k, v := range r.cache {
				if first || v.expiresAt.Before(oldestExp) {
					oldestKey, oldestExp, first = k, v.expiresAt, false
				}
			}
			if oldestKey != "" {
				delete(r.cache, oldestKey)
			}
		}
	}
	r.cache[clientID] = cimdCacheEntry{
		client: Client{
			ID:           client.ID,
			Name:         client.Name,
			RedirectURIs: slices.Clone(client.RedirectURIs),
		},
		expiresAt: expiresAt,
	}
}
