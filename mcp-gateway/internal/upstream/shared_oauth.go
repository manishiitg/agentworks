package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SharedOAuth reuses the host product's credential store/refresh manager.
// Only this fixed, operator-configured origin receives the service secret.
func SharedOAuth(origin, secret string) (func(context.Context, string, string, string) (string, error), error) {
	u, err := url.Parse(strings.TrimRight(origin, "/"))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("GATEWAY_PRODUCT_URL must be an HTTPS or loopback origin")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, errors.New("shared OAuth requires HTTPS or loopback")
	}
	if len(secret) < 32 {
		return nil, errors.New("shared OAuth requires a service credential")
	}
	u.Path = "/internal/caplayer/oauth-token"
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, name, resource, connectionID string) (string, error) {
		payload, _ := json.Marshal(map[string]string{"server_name": name, "url": resource, "connection_id": connectionID})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return "", errors.New("shared OAuth service unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", errors.New("shared OAuth authorization required")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
		if err != nil || len(body) > 16384 {
			return "", errors.New("invalid shared OAuth response")
		}
		var result struct {
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal(body, &result) != nil || result.AccessToken == "" || strings.ContainsAny(result.AccessToken, "\r\n") {
			return "", errors.New("invalid shared OAuth response")
		}
		return result.AccessToken, nil
	}, nil
}
