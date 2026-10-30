package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-219: separate launch must not publish proof into a shared directory
// or replace a symlink/regular file while trying to recover a stale socket.
func TestDesktopSocketRefusesUnsafePaths(t *testing.T) {
	for _, kind := range []string{"shared-directory", "symlink", "regular-file", "non-loopback"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "desktop.sock")
			address := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18743}
			switch kind {
			case "shared-directory":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "target"), path); err != nil {
					t.Fatal(err)
				}
			case "regular-file":
				if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "non-loopback":
				address.IP = net.ParseIP("0.0.0.0")
			}
			t.Setenv("AGENTWORKS_DESKTOP_SOCKET", path)
			listener, err := startDesktopSocket(address)
			if listener != nil {
				listener.Close()
			}
			if err == nil {
				t.Fatal("unsafe socket path accepted")
			}
			if kind == "symlink" || kind == "regular-file" {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("existing path removed")
				}
			}
		})
	}
	previous := desktopStartup
	desktopStartup.Secret = "pipe-proof"
	defer func() { desktopStartup = previous }()
	t.Setenv("AGENTWORKS_DESKTOP_SOCKET", filepath.Join(t.TempDir(), "unused.sock"))
	listener, err := startDesktopSocket(&net.TCPAddr{IP: net.ParseIP("0.0.0.0")})
	if listener != nil || err != nil {
		t.Fatal("fd3 launch must not start a socket")
	}
}
