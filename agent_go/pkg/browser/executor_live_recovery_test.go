package browser

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// Live check against a real workspace service and Chrome (a server's own account, after a deploy). Skipped unless
// AW_LIVE_BROWSER_WORKSPACE_URL is set, plus WORKSPACE_API_TOKEN and AGENT_BROWSER_SHARED_PROFILE as the service has them and
// AW_LIVE_BROWSER_FOLDER (a docs-relative folder the command may write, e.g. tmp/zz-live).
func TestLiveExecutorRecoversFromACrashedTabAndRefusesTallScreenshots(t *testing.T) {
	url := os.Getenv("AW_LIVE_BROWSER_WORKSPACE_URL")
	folder := os.Getenv("AW_LIVE_BROWSER_FOLDER")
	if url == "" || folder == "" {
		t.Skip("set AW_LIVE_BROWSER_WORKSPACE_URL and AW_LIVE_BROWSER_FOLDER for the live browser check")
	}
	t.Setenv(EnvAgentBrowserCDPEnabled, "false")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Tall</title><body style='margin:0'><div style='height:600000px;background:linear-gradient(red,blue)'>tall</div>"))
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	tall := "http://" + listener.Addr().String() + "/"

	session := os.Getenv("AW_LIVE_BROWSER_SESSION")
	if session == "" {
		session = "project-0a1b2c3d4e5f6071--browser"
	}
	ctx := context.WithValue(context.Background(), common.FolderGuardAllowedWriteFolderKey, []string{folder})
	if userID := os.Getenv("AW_LIVE_BROWSER_USER_ID"); userID != "" { // multi-user servers: the X-User-ID the app sends
		ctx = context.WithValue(ctx, common.UserIDKey, userID)
	}
	executor := NewExecutor(NewClient(url))
	run := func(command string, args ...string) (string, error) {
		callCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
		defer cancel()
		return executor.HandleAgentBrowser(callCtx, map[string]interface{}{"command": command, "args": args, "session": session})
	}
	defer func() {
		_, _ = run("close")
		if dir := browserconfig.SocketDirForSession(session); dir != browserconfig.SocketRoot {
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Errorf("socket folder %s left behind after close", dir)
			}
		}
	}()

	if _, err := run("open", "https://example.com"); err != nil {
		t.Fatalf("first open: %v", err)
	}
	// The crashed tab: this open may fail or hang, which is the situation to recover from.
	_, crashErr := run("open", "chrome://crash")
	t.Logf("chrome://crash: %v", crashErr)
	out, err := run("open", "https://example.com")
	if err != nil {
		t.Fatalf("open after a crashed tab did not recover by itself: %v", err)
	}
	if !strings.Contains(out, "success") {
		t.Fatalf("unexpected open output %q", out)
	}
	if _, err := run("open", tall); err != nil {
		t.Fatalf("open tall page: %v", err)
	}
	_, err = run("screenshot", "--full", folder+"/tall.png")
	if err == nil || !strings.Contains(err.Error(), "SCREENSHOT_TOO_TALL") {
		t.Fatalf("a 600000px full-page screenshot must be refused with SCREENSHOT_TOO_TALL, got %v", err)
	}
	// The refusal must not have touched the browser: the same page is still usable.
	if _, err := run("snapshot"); err != nil {
		t.Fatalf("browser unusable after the refused screenshot: %v", err)
	}
}
