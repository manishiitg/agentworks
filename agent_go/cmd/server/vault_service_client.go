package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// One way to call the Vault service from this server: its URL and service credential, the shared connection pool
// (capLayerTransport), no redirects, a per-call timeout and a response size limit. Callers judge the status.

var (
	errVaultServiceNotConfigured = errors.New("Vault service is not configured")
	errVaultServiceUnavailable   = errors.New("Vault service unavailable")
	errVaultResponseTooLarge     = errors.New("Vault response exceeded limit")
)

// vaultServiceHTTPClient is an HTTP client on the shared Vault connection pool. Redirects are never followed.
func vaultServiceHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: capLayerTransport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type vaultServiceCall struct {
	Method, Path string
	Body         []byte
	Actor        string            // X-CapLayer-Actor; empty sends none
	Headers      map[string]string // extra request headers
	Timeout      time.Duration
	Limit        int64 // largest response body accepted, in bytes
	ProductGate  bool  // also require the Vault product to be switched on
	Changes      bool  // a successful call changes what Team cards show about Vault
}

// do sends the call and returns the status and body. A body over Limit returns the status with
// errVaultResponseTooLarge.
func (c vaultServiceCall) do(ctx context.Context) (int, []byte, error) {
	config := gatewayAdminServiceConfig
	if c.ProductGate {
		config = capLayerServiceConfig
	}
	target, secret, err := config()
	if err != nil {
		return 0, nil, errVaultServiceNotConfigured
	}
	target.Path = strings.TrimRight(target.Path, "/") + c.Path
	req, err := http.NewRequestWithContext(ctx, c.Method, target.String(), bytes.NewReader(c.Body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	if c.Method != http.MethodGet && c.Method != http.MethodHead {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Actor != "" {
		req.Header.Set("X-CapLayer-Actor", c.Actor)
	}
	for name, value := range c.Headers {
		req.Header.Set(name, value)
	}
	resp, err := vaultServiceHTTPClient(c.Timeout).Do(req)
	if err != nil {
		return 0, nil, errVaultServiceUnavailable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.Limit+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if int64(len(body)) > c.Limit {
		return resp.StatusCode, nil, errVaultResponseTooLarge
	}
	if c.Changes && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		invalidateCrewTeamVaultDisplays()
	}
	return resp.StatusCode, body, nil
}

// vaultSyncDirectoryUser binds one directory account to its Vault user record. Called only after the platform's
// current administrator check; it does not grant the Vault surface. Any 2xx is success.
func vaultSyncDirectoryUser(ctx context.Context, id, email string) error {
	payload, err := json.Marshal(map[string]string{"ID": id, "Email": email})
	if err != nil {
		return err
	}
	status, _, err := vaultServiceCall{Method: http.MethodPost, Path: "/api/admin/users/sync", Body: payload, Timeout: 10 * time.Second, Limit: 8192}.do(ctx)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return errors.New("directory synchronization refused")
	}
	return nil
}
