package agentworksclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const cliOAuthClientID = "agentworks-cli"

type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	UserCode                string `json:"user_code"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (c *Client) oauthResource() string { return c.baseURL + "/api/external/v1" }

func (c *Client) StartDeviceAuthorization(ctx context.Context, scopes ...string) (DeviceAuthorization, error) {
	data, err := c.request(ctx, http.MethodPost, "/api/oauth/cli/device", map[string]any{"scopes": scopes}, false)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	var result DeviceAuthorization
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	if !strings.HasPrefix(result.DeviceCode, "cli_device_") || result.VerificationURIComplete == "" || len(result.UserCode) != 8 || result.ExpiresIn < 1 || result.ExpiresIn > 900 || result.Interval < 1 || result.Interval > 30 {
		return result, errors.New("server returned invalid device authorization")
	}
	for _, digit := range result.UserCode {
		if !strings.ContainsRune("0123456789ABCDEF", digit) {
			return result, errors.New("server returned invalid sign-in code")
		}
	}
	verification, err := url.Parse(result.VerificationURIComplete)
	if err != nil || verification.User != nil || verification.Fragment != "" || verification.Path != "/oauth/cli" || !c.safeVerificationOrigin(verification) {
		return result, errors.New("server returned an unsafe verification URL")
	}
	code := verification.Query().Get("code")
	if len(code) != len("cli_verify_")+64 || !strings.HasPrefix(code, "cli_verify_") || strings.ToUpper(code[len("cli_verify_"):len("cli_verify_")+8]) != result.UserCode {
		return result, errors.New("server returned a mismatched sign-in code")
	}
	return result, nil
}

func (c *Client) safeVerificationOrigin(verification *url.URL) bool {
	if verification.Scheme+"://"+verification.Host == c.baseURL {
		return true
	}
	server, err := url.Parse(c.baseURL)
	if err != nil || server.Scheme != "http" || verification.Scheme != "http" {
		return false
	}
	return oauthLoopbackHost(server.Hostname()) && oauthLoopbackHost(verification.Hostname())
}

func oauthLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) oauthForm(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, errors.New("OAuth response is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &body)
		if body.Error == "" {
			body.Error = "http_error"
		}
		return nil, &APIError{Status: resp.StatusCode, Code: body.Error, Message: body.Error}
	}
	return data, nil
}

func parseOAuthTokens(data []byte) (OAuthTokens, error) {
	var tokens OAuthTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return tokens, err
	}
	if !strings.HasPrefix(tokens.AccessToken, "aw_cli_") || strings.HasPrefix(tokens.AccessToken, "aw_cli_refresh_") || !strings.HasPrefix(tokens.RefreshToken, "aw_cli_refresh_") || tokens.ExpiresIn < 60 || tokens.ExpiresIn > 86400 {
		return tokens, errors.New("server returned invalid CLI credentials")
	}
	return tokens, nil
}

func (c *Client) PollDeviceAuthorization(ctx context.Context, deviceCode string) (OAuthTokens, error) {
	data, err := c.oauthForm(ctx, "/api/oauth/cli/token", url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {cliOAuthClientID}, "resource": {c.oauthResource()}, "device_code": {deviceCode}})
	if err != nil {
		return OAuthTokens{}, err
	}
	return parseOAuthTokens(data)
}

func (c *Client) RefreshOAuth(ctx context.Context, refresh string) (OAuthTokens, error) {
	data, err := c.oauthForm(ctx, "/api/oauth/cli/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {cliOAuthClientID}, "resource": {c.oauthResource()}, "refresh_token": {refresh}})
	if err != nil {
		return OAuthTokens{}, err
	}
	return parseOAuthTokens(data)
}

func (c *Client) RevokeOAuth(ctx context.Context, refresh string) error {
	_, err := c.oauthForm(ctx, "/api/oauth/cli/revoke", url.Values{"token": {refresh}})
	return err
}

// ConfigTokenProvider serializes rotation across CLI and stdio MCP processes.
func ConfigTokenProvider(server, path string) func(context.Context) (string, error) {
	var mu sync.Mutex
	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		cfg, err := LoadConfig(path)
		if err != nil {
			return "", err
		}
		if cfg.Server != server {
			return "", errors.New("saved login belongs to another server; run agentworks login")
		}
		if cfg.RefreshToken == "" {
			if cfg.Token == "" {
				return "", errors.New("not logged in: run agentworks login")
			}
			return cfg.Token, nil
		}
		if cfg.Token != "" && time.Until(cfg.ExpiresAt) > 2*time.Minute {
			return cfg.Token, nil
		}
		lockPath := path + ".lock"
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		if info, err := os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() {
			return "", errors.New("credential lock must be a regular file")
		}
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return "", err
		}
		defer lock.Close()
		unlock, err := lockFileExclusive(lock)
		if err != nil {
			return "", err
		}
		defer unlock()
		cfg, err = LoadConfig(path)
		if err != nil {
			return "", err
		}
		if cfg.Server != server || cfg.RefreshToken == "" {
			return "", errors.New("saved login changed; run agentworks login")
		}
		if cfg.Token != "" && time.Until(cfg.ExpiresAt) > 2*time.Minute {
			return cfg.Token, nil
		}
		client, err := New(server, "")
		if err != nil {
			return "", err
		}
		tokens, err := client.RefreshOAuth(ctx, cfg.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("refresh browser login: %w (run agentworks login again)", err)
		}
		cfg.Token = tokens.AccessToken
		cfg.RefreshToken = tokens.RefreshToken
		cfg.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		if err := SaveConfig(path, cfg); err != nil {
			return "", err
		}
		return cfg.Token, nil
	}
}
