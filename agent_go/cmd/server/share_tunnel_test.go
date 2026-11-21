package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeStubCloudflared installs a fake cloudflared that prints a quick-tunnel
// URL to stderr, then sleeps until killed.
func writeStubCloudflared(t *testing.T, url string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cloudflared")
	script := "#!/bin/sh\necho '2026-01-01 INF Your quick Tunnel has been created! Visit it at (" + url + "):' >&2\nsleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDFLARED_BIN", path)
}

func resetShareTunnel(t *testing.T) {
	t.Helper()
	StopShareTunnel()
	t.Cleanup(func() { StopShareTunnel() })
}

func TestShareTunnelStartStopStatus(t *testing.T) {
	writeStubCloudflared(t, "https://bright-otter-42.trycloudflare.com")
	resetShareTunnel(t)
	SetShareTunnelServerPort(8123)

	if status := GetShareTunnelStatus(); status.Active {
		t.Fatalf("tunnel active before start: %+v", status)
	}
	status, err := StartShareTunnel(context.Background(), "owner", time.Hour)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !status.Active || status.PublicURL != "https://bright-otter-42.trycloudflare.com" || status.StartedBy != "owner" {
		t.Fatalf("status after start = %+v", status)
	}
	if got := effectiveShareBaseURL(); got != "https://bright-otter-42.trycloudflare.com" {
		t.Fatalf("effective base URL = %q", got)
	}

	// Starting again returns the existing tunnel instead of a second process.
	again, err := StartShareTunnel(context.Background(), "owner", time.Hour)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if again.PublicURL != status.PublicURL {
		t.Fatalf("second start returned a different tunnel: %+v", again)
	}

	if stopped := StopShareTunnel(); stopped.Active {
		t.Fatalf("tunnel still active after stop: %+v", stopped)
	}
	t.Setenv("PUBLIC_URL", "https://deploy.example/")
	if got := effectiveShareBaseURL(); got != "https://deploy.example" {
		t.Fatalf("effective base URL after stop = %q", got)
	}
}

func TestShareTunnelRequiresBinary(t *testing.T) {
	resetShareTunnel(t)
	SetShareTunnelServerPort(8123)
	t.Setenv("CLOUDFLARED_BIN", filepath.Join(t.TempDir(), "missing-cloudflared"))
	if _, err := StartShareTunnel(context.Background(), "owner", time.Hour); err == nil {
		t.Fatal("start succeeded with a missing binary")
	}
}

func TestShareTunnelExpires(t *testing.T) {
	writeStubCloudflared(t, "https://short-lived-1.trycloudflare.com")
	resetShareTunnel(t)
	SetShareTunnelServerPort(8123)

	if _, err := StartShareTunnel(context.Background(), "owner", 100*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for GetShareTunnelStatus().Active && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if GetShareTunnelStatus().Active {
		t.Fatal("tunnel still active after expiry")
	}
}

func TestManageInternetShareToolIsAdminGated(t *testing.T) {
	f := newExternalToolsFixture(t)
	reg := &recordingRegistrar{}
	if err := f.api.registerShareTunnelTools(reg, "someone"); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.tools["manage_internet_share"]
	if !ok || !strings.Contains(tool.desc, "WHOLE server") {
		t.Fatalf("manage_internet_share registration = %+v", tool)
	}

	// Multi-user without a directory record is not an admin.
	t.Setenv("MULTI_USER_MODE", "true")
	if _, err := tool.exec(context.Background(), map[string]interface{}{"action": "status"}); err == nil || !strings.Contains(err.Error(), "admin required") {
		t.Fatalf("non-admin status err = %v", err)
	}
}

func TestManageInternetShareToolStatusAndStop(t *testing.T) {
	writeStubCloudflared(t, "https://tool-driven-9.trycloudflare.com")
	resetShareTunnel(t)
	SetShareTunnelServerPort(8123)

	f := newExternalToolsFixture(t)
	// The fixture pins multi-user mode and seeds "owner" without admin;
	// single-user with a record-less identity is the admin path.
	t.Setenv("MULTI_USER_MODE", "false")
	reg := &recordingRegistrar{}
	if err := f.api.registerShareTunnelTools(reg, "site-owner"); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools["manage_internet_share"]

	out, err := tool.exec(context.Background(), map[string]interface{}{"action": "status"})
	if err != nil || !strings.Contains(out, "share_tunnel=stopped") {
		t.Fatalf("status before start = %q err=%v", out, err)
	}
	out, err = tool.exec(context.Background(), map[string]interface{}{"action": "start", "duration_minutes": float64(30)})
	if err != nil || !strings.Contains(out, "public_url=https://tool-driven-9.trycloudflare.com") {
		t.Fatalf("start = %q err=%v", out, err)
	}
	out, err = tool.exec(context.Background(), map[string]interface{}{"action": "stop"})
	if err != nil || !strings.Contains(out, "share_tunnel=stopped") {
		t.Fatalf("stop = %q err=%v", out, err)
	}
}
