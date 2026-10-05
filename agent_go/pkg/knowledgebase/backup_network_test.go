package knowledgebase

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestBackupNetworkRejectsPrivateHostsAndPinsDNS(t *testing.T) {
	s, admin, _ := fixture(t, false)
	admin.AccessOnly = true
	for _, remote := range []string{
		"https://localhost/repo.git", "https://private.localhost/repo.git", "https://127.0.0.1/repo.git", "https://10.0.0.1/repo.git", "https://169.254.169.254/repo.git", "https://100.64.1.2/repo.git", "https://[::ffff:127.0.0.1]/repo.git", "https://[fd00::1]/repo.git", "https://192.0.2.1/repo.git", "git@github.com:org/repo.git", "ssh://git@git.example/repo.git",
	} {
		mcpError(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "configure_backup", "remote_url": remote, "username": "git", "request_id": "unsafe-setup"}, "INVALID_ARGUMENT")
	}
	remote := backupGitCredential{remote: "https://git.example/repo.git"}
	for _, addresses := range [][]netip.Addr{
		{netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}, {netip.MustParseAddr("2001:db8::1")}, {},
	} {
		lookup := func(context.Context, string) ([]netip.Addr, error) { return addresses, nil }
		if pin, err := backupNetworkResolve(t.Context(), remote, lookup); err == nil || pin != "" {
			t.Fatalf("unsafe DNS accepted: %v %s %v", addresses, pin, err)
		}
	}
	lookup := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	if pin, err := backupNetworkResolve(t.Context(), remote, lookup); err != nil || pin != "git.example:443:8.8.8.8" {
		t.Fatal(pin, err)
	}
	// A later lookup may have changed: validate again before each transport.
	lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	}
	if _, err := backupNetworkResolve(t.Context(), remote, lookup); err == nil {
		t.Fatal("rebound host accepted")
	}
	lookup = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("DNS unavailable") }
	if _, err := backupNetworkResolve(t.Context(), remote, lookup); err == nil {
		t.Fatal("failed DNS accepted")
	}
	if _, err := backupNetworkResolve(t.Context(), backupGitCredential{remote: "git@github.com:org/repo.git", deployment: true}, lookup); err != nil {
		t.Fatal("operator SSH denied", err)
	}
}
