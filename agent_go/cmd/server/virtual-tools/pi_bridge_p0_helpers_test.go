//go:build generate_text_llm_pi_bridge_p0_live

package virtualtools

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func startPiBridgeExecutor(t *testing.T) (string, string) {
	t.Helper()
	token := executor.GenerateAPIToken()
	handlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		tool := strings.TrimPrefix(r.URL.Path, "/tools/custom/")
		handlers.HandlePerToolCustomRequest(w, r, tool)
	})
	mux.HandleFunc("/tools/virtual/", func(w http.ResponseWriter, r *http.Request) {
		tool := strings.TrimPrefix(r.URL.Path, "/tools/virtual/")
		handlers.HandlePerToolVirtualRequest(w, r, tool)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen Pi bridge executor: %v", err)
	}
	server := &http.Server{Handler: executor.AuthMiddleware(token)(mux)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return "http://" + listener.Addr().String(), token
}

func buildPiBridgeBinary(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	bridgeSource := filepath.Clean(filepath.Join(wd, "../../../../../mcpagent/cmd/mcpbridge"))
	if _, err := os.Stat(bridgeSource); err != nil {
		t.Fatalf("locate mcpagent bridge source %q: %v", bridgeSource, err)
	}
	bin := filepath.Join(t.TempDir(), "mcpbridge")
	output, err := exec.Command("go", "build", "-o", bin, bridgeSource).CombinedOutput()
	if err != nil {
		t.Fatalf("build mcpagent bridge: %v\n%s", err, output)
	}
	return bin
}
