package oauth

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"time"
)

// CallbackServer is a temporary HTTP server that listens for OAuth callbacks
type CallbackServer struct {
	server   *http.Server
	port     int
	codeChan chan string
	errChan  chan error
	state    string // Expected state parameter for CSRF protection
}

// NewCallbackServer creates a new callback server on the specified port
func NewCallbackServer(port int, expectedState string) *CallbackServer {
	cs := &CallbackServer{
		port:     port,
		codeChan: make(chan string, 1),
		errChan:  make(chan error, 1),
		state:    expectedState,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handleCallback)

	cs.server = &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return cs
}

// WaitForCallback starts the server and waits for an OAuth callback.
// It automatically shuts down the server after receiving the callback or on timeout.
// Returns the authorization code or an error.
func (cs *CallbackServer) WaitForCallback(ctx context.Context) (string, error) {
	// Start server in background
	go func() {
		if err := cs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			cs.sendErr(fmt.Errorf("callback server error: %w", err))
		}
	}()

	// Ensure server shuts down when done
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cs.server.Shutdown(shutdownCtx)
	}()

	// Wait for callback, error, or timeout
	select {
	case code := <-cs.codeChan:
		return code, nil
	case err := <-cs.errChan:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", ErrCallbackTimeout
	}
}

// sendCode delivers the authorization code without blocking duplicate requests.
func (cs *CallbackServer) sendCode(code string) {
	select {
	case cs.codeChan <- code:
	default:
	}
}

// sendErr delivers a callback error without blocking duplicate requests.
func (cs *CallbackServer) sendErr(err error) {
	select {
	case cs.errChan <- err:
	default:
	}
}

// handleCallback processes the OAuth callback request
func (cs *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	query := r.URL.Query()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Check for error from OAuth provider
	if errCode := query.Get("error"); errCode != "" {
		errDesc := query.Get("error_description")
		if errDesc == "" {
			errDesc = errCode
		}

		// Send error page to browser
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head><title>Authentication Failed</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Authentication Failed</h1>
	<p>%s</p>
	<p>You can close this window.</p>
</body>
</html>`, html.EscapeString(errDesc))

		if errCode == "access_denied" {
			cs.sendErr(ErrAuthDenied)
		} else {
			cs.sendErr(fmt.Errorf("OAuth error: %s - %s", errCode, errDesc))
		}
		return
	}

	// Validate state parameter (CSRF protection)
	state := query.Get("state")
	if cs.state != "" && state != cs.state {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>Authentication Failed</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Security Error</h1>
	<p>Invalid state parameter. Possible CSRF attack.</p>
	<p>You can close this window.</p>
</body>
</html>`)

		cs.sendErr(ErrInvalidState)
		return
	}

	// Extract authorization code
	code := query.Get("code")
	if code == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>Authentication Failed</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Authentication Failed</h1>
	<p>No authorization code received.</p>
	<p>You can close this window.</p>
</body>
</html>`)

		cs.sendErr(fmt.Errorf("no authorization code in callback"))
		return
	}

	// Send success page to browser
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>Authentication Successful</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>✅ Authentication Successful!</h1>
	<p>You have been authenticated successfully.</p>
	<p>You can close this window and return to the terminal.</p>
	<script>
		// Auto-close after 3 seconds
		setTimeout(function() {
			window.close();
		}, 3000);
	</script>
</body>
</html>`)

	// Send code to waiting channel without blocking duplicate hits
	cs.sendCode(code)
}

// GetCallbackURL returns the full callback URL for this server
func (cs *CallbackServer) GetCallbackURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", cs.port)
}
