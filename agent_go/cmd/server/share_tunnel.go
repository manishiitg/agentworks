package server

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Internet share tunnel: exposes this server through a Cloudflare quick
// tunnel (https://xxx.trycloudflare.com) so get_report_link/get_file_link
// URLs become reachable off-machine. The link builders prefer the tunnel URL
// over PUBLIC_URL while one is active.
//
// Ephemeral by design: the URL dies with the tunnel process. Starting one
// exposes the WHOLE server (every route), not a single report — so this is
// admin-gated with a default 1h expiry, and the tool description says so.
type shareTunnelManager struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	publicURL  string
	startedAt  time.Time
	expiresAt  time.Time
	startedBy  string
	serverPort int
	expiryTimer *time.Timer
}

var shareTunnel = &shareTunnelManager{}

// tryCloudflareURLRe matches the public URL cloudflared prints for a quick
// tunnel: "Your quick Tunnel has been created! ..." followed by a
// https://xxx.trycloudflare.com line (no dots — trycloudflare.com, verified
// against cloudflared 2025.9.0 output).
var tryCloudflareURLRe = regexp.MustCompile(`https://[A-Za-z0-9-]+\.trycloudflare\.com`)

// SetShareTunnelServerPort records the server's actual bound port (dynamic
// ports make the configured value wrong). Called once at startup.
func SetShareTunnelServerPort(port int) {
	shareTunnel.mu.Lock()
	defer shareTunnel.mu.Unlock()
	shareTunnel.serverPort = port
}

// shareTunnelBinaryPath resolves the cloudflared binary. CLOUDFLARED_BIN
// overrides the PATH lookup (used by tests to inject a stub).
func shareTunnelBinaryPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("CLOUDFLARED_BIN")); override != "" {
		return override, nil
	}
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return "", fmt.Errorf("cloudflared is not installed (install with `brew install cloudflared` or from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)")
	}
	return path, nil
}

// ShareTunnelStatus is the JSON shape the tool and status endpoint return.
type ShareTunnelStatus struct {
	Active    bool   `json:"active"`
	PublicURL string `json:"public_url,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	StartedBy string `json:"started_by,omitempty"`
}

// GetShareTunnelStatus reports the current tunnel state. Safe for concurrent use.
func GetShareTunnelStatus() ShareTunnelStatus {
	shareTunnel.mu.Lock()
	defer shareTunnel.mu.Unlock()
	if shareTunnel.cmd == nil {
		return ShareTunnelStatus{Active: false}
	}
	return ShareTunnelStatus{
		Active:    true,
		PublicURL: shareTunnel.publicURL,
		StartedAt: shareTunnel.startedAt.UTC().Format(time.RFC3339),
		ExpiresAt: shareTunnel.expiresAt.UTC().Format(time.RFC3339),
		StartedBy: shareTunnel.startedBy,
	}
}

// effectiveShareBaseURL prefers the active tunnel URL over PUBLIC_URL. Only
// the share-link builders use it; OAuth/Gmail keep reading env directly so a
// temporary tunnel never rewrites their redirect/callback hosts.
func effectiveShareBaseURL() string {
	shareTunnel.mu.Lock()
	url := shareTunnel.publicURL
	shareTunnel.mu.Unlock()
	if url != "" {
		return url
	}
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
}

// StartShareTunnel launches a cloudflared quick tunnel against this server
// and waits (up to 45s) for its public URL. Idempotent: an active tunnel is
// returned as-is. duration caps how long the tunnel lives; <=0 means 1h.
func StartShareTunnel(ctx context.Context, userID string, duration time.Duration) (ShareTunnelStatus, error) {
	shareTunnel.mu.Lock()
	if shareTunnel.cmd != nil {
		status := ShareTunnelStatus{
			Active:    true,
			PublicURL: shareTunnel.publicURL,
			StartedAt: shareTunnel.startedAt.UTC().Format(time.RFC3339),
			ExpiresAt: shareTunnel.expiresAt.UTC().Format(time.RFC3339),
			StartedBy: shareTunnel.startedBy,
		}
		shareTunnel.mu.Unlock()
		return status, nil
	}
	port := shareTunnel.serverPort
	shareTunnel.mu.Unlock()

	if port <= 0 {
		return ShareTunnelStatus{}, fmt.Errorf("server port is unknown; the tunnel cannot start")
	}
	binary, err := shareTunnelBinaryPath()
	if err != nil {
		return ShareTunnelStatus{}, err
	}
	if duration <= 0 {
		duration = time.Hour
	}
	if duration > 8*time.Hour {
		duration = 8 * time.Hour
	}

	cmd := exec.CommandContext(ctx, binary, "tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", port))
	cmd.Env = minimalChildEnv()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ShareTunnelStatus{}, fmt.Errorf("tunnel stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return ShareTunnelStatus{}, fmt.Errorf("start cloudflared: %w", err)
	}

	// Drain stderr continuously: cloudflared keeps logging, and a full pipe
	// would stall it. The scanner below reads the public URL off this stream.
	urlCh := make(chan string, 1)
	errCh := make(chan string, 1)
	go func() {
		defer close(urlCh)
		defer close(errCh)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		var tail []string
		for scanner.Scan() {
			line := scanner.Text()
			if url := tryCloudflareURLRe.FindString(line); url != "" {
				select {
				case urlCh <- url:
				default:
				}
			}
			tail = append(tail, line)
			if len(tail) > 10 {
				tail = tail[1:]
			}
		}
		errCh <- strings.Join(tail, "\n")
	}()
	go func() {
		// Reap the process; a tunnel that dies on its own must clear the
		// override so links stop advertising a dead URL.
		_ = cmd.Wait()
		shareTunnel.mu.Lock()
		if shareTunnel.cmd == cmd {
			shareTunnel.clearLocked()
		}
		shareTunnel.mu.Unlock()
	}()

	timeout, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	select {
	case <-timeout.Done():
		_ = cmd.Process.Kill()
		return ShareTunnelStatus{}, fmt.Errorf("cloudflared did not print a tunnel URL within 45s")
	case tail := <-errCh:
		_ = cmd.Process.Kill()
		return ShareTunnelStatus{}, fmt.Errorf("cloudflared exited before printing a tunnel URL: %s", tail)
	case url := <-urlCh:
		now := time.Now()
		shareTunnel.mu.Lock()
		shareTunnel.cmd = cmd
		shareTunnel.publicURL = url
		shareTunnel.startedAt = now
		shareTunnel.expiresAt = now.Add(duration)
		shareTunnel.startedBy = userID
		if shareTunnel.expiryTimer != nil {
			shareTunnel.expiryTimer.Stop()
		}
		shareTunnel.expiryTimer = time.AfterFunc(duration, func() {
			log.Printf("[SHARE_TUNNEL] tunnel expired after %v; stopping", duration)
			StopShareTunnel()
		})
		status := ShareTunnelStatus{
			Active:    true,
			PublicURL: url,
			StartedAt: now.UTC().Format(time.RFC3339),
			ExpiresAt: shareTunnel.expiresAt.UTC().Format(time.RFC3339),
			StartedBy: userID,
		}
		shareTunnel.mu.Unlock()
		log.Printf("[SHARE_TUNNEL] started by %s: %s (expires %v)", userID, url, duration)
		return status, nil
	}
}

// StopShareTunnel kills the tunnel process and clears the URL override. It is
// safe to call with no active tunnel.
func StopShareTunnel() ShareTunnelStatus {
	shareTunnel.mu.Lock()
	defer shareTunnel.mu.Unlock()
	if shareTunnel.cmd == nil {
		return ShareTunnelStatus{Active: false}
	}
	if shareTunnel.expiryTimer != nil {
		shareTunnel.expiryTimer.Stop()
		shareTunnel.expiryTimer = nil
	}
	_ = shareTunnel.cmd.Process.Kill()
	shareTunnel.clearLocked()
	log.Printf("[SHARE_TUNNEL] stopped")
	return ShareTunnelStatus{Active: false}
}

func (m *shareTunnelManager) clearLocked() {
	m.cmd = nil
	m.publicURL = ""
	if m.expiryTimer != nil {
		m.expiryTimer.Stop()
		m.expiryTimer = nil
	}
}

// registerShareTunnelTools exposes internet-share management to Builder chat.
// Sharing is admin-gated per invocation: a tunnel exposes every server route,
// not just one report.
func (api *StreamingAPI) registerShareTunnelTools(reg definitionToolRegistrar, userID string) error {
	return reg.RegisterCustomTool("manage_internet_share", "Start, inspect, or stop a temporary Cloudflare quick tunnel (https://xxx.trycloudflare.com) that puts THIS AgentWorks server on the internet so get_report_link/get_file_link URLs become reachable off-machine. SECURITY: a tunnel exposes the WHOLE server (every route), not one report — links still require sign-in and workflow access, there are no anonymous links, and tunnel traffic transits Cloudflare. Ephemeral by design: links die with the tunnel (default 1h expiry, max 8h) and a restart mints a new URL. Admin-only; every invocation checks current caller permissions. Use action=status to check state before starting; never start a tunnel without the user's explicit request.", map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"action":           map[string]interface{}{"type": "string", "enum": []string{"start", "stop", "status"}},
			"duration_minutes": map[string]interface{}{"type": "integer", "description": "Tunnel lifetime for start; default 60, max 480."},
		}, "required": []string{"action"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		claims := &UserClaims{UserID: userID}
		access := userAccessForClaims(claims)
		if access.Disabled {
			return "", fmt.Errorf("Access denied: account is disabled")
		}
		if !access.Admin {
			return "", fmt.Errorf("Access denied: admin required to expose this server to the internet")
		}
		action, _ := args["action"].(string)
		switch action {
		case "status":
			return shareTunnelStatusJSON(GetShareTunnelStatus()), nil
		case "stop":
			return shareTunnelStatusJSON(StopShareTunnel()), nil
		case "start":
			duration := time.Hour
			if raw, ok := args["duration_minutes"].(float64); ok && raw > 0 {
				duration = time.Duration(raw) * time.Minute
			}
			status, err := StartShareTunnel(ctx, userID, duration)
			if err != nil {
				return "", err
			}
			return shareTunnelStatusJSON(status), nil
		default:
			return "", fmt.Errorf("action must be start, stop, or status")
		}
	}, "user_management")
}

// handleGetShareTunnelStatus serves the Publish panel's internet-share
// section. Admin-only: the URL reveals an exposed server.
func (api *StreamingAPI) handleGetShareTunnelStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, GetShareTunnelStatus())
}

func shareTunnelStatusJSON(status ShareTunnelStatus) string {
	active := "stopped"
	if status.Active {
		active = "active"
	}
	parts := []string{"share_tunnel=" + active}
	if status.Active {
		parts = append(parts, "public_url="+status.PublicURL, "expires_at="+status.ExpiresAt, "started_by="+status.StartedBy)
	}
	return strings.Join(parts, " ")
}
