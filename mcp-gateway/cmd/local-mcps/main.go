// local-mcps exposes the official filesystem and memory reference servers on
// loopback HTTP for development. The reference servers use stdio; this bridge
// forwards their original tool definitions and calls without changing policy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func bridge(ctx context.Context, node, runtimeDir, name, endpoint string, env, args []string) (http.Handler, func(), int, error) {
	entry := filepath.Join(runtimeDir, "node_modules", "@modelcontextprotocol", "server-"+name, "dist", "index.js")
	if _, err := os.Stat(entry); err != nil {
		return nil, nil, 0, fmt.Errorf("install reference packages into %s first: %w", runtimeDir, err)
	}
	c, err := client.NewStdioMCPClient(node, env, append([]string{entry}, args...)...)
	if err != nil {
		return nil, nil, 0, err
	}
	closeClient := func() { _ = c.Close() }
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "caplayer-local-reference-bridge", Version: "0.1.0"}
	if _, err := c.Initialize(ctx, init); err != nil {
		closeClient()
		return nil, nil, 0, err
	}
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		closeClient()
		return nil, nil, 0, err
	}
	srv := server.NewMCPServer("local-"+name, "0.1.0", server.WithToolCapabilities(false))
	for _, tool := range tools.Tools {
		srv.AddTool(tool, func(callCtx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			callCtx, cancel := context.WithTimeout(callCtx, 30*time.Second)
			defer cancel()
			return c.CallTool(callCtx, req)
		})
	}
	return server.NewStreamableHTTPServer(srv, server.WithEndpointPath(endpoint), server.WithStateLess(true)), closeClient, len(tools.Tools), nil
}

func run() error {
	dataDefault := filepath.Join(os.TempDir(), "caplayer-reference-mcps")
	port := flag.Int("port", 18164, "loopback HTTP port")
	dataDir := flag.String("data-dir", dataDefault, "dedicated local test data directory")
	runtimeDir := flag.String("runtime-dir", filepath.Join(dataDefault, "runtime"), "directory containing installed npm reference packages")
	node := flag.String("node", "node", "Node.js executable")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		return errors.New("invalid port")
	}
	root, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	files := filepath.Join(root, "files")
	if err := os.MkdirAll(files, 0700); err != nil {
		return err
	}
	// Create a sample once; never overwrite files the user changes in tests.
	sample, err := os.OpenFile(filepath.Join(files, "README.md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, writeErr := sample.WriteString("# CapLayer local MCP test\n\nOnly this test folder is accessible to the filesystem server.\nSynthetic PII sample: test.person@example.test\n")
		closeErr := sample.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !os.IsExist(err) {
		return err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	total := 0
	for _, config := range []struct {
		name      string
		env, args []string
	}{
		{name: "filesystem", args: []string{files}},
		{name: "memory", env: []string{"MEMORY_FILE_PATH=" + filepath.Join(root, "memory.jsonl")}},
	} {
		endpoint := "/" + config.name + "/mcp"
		handler, cleanup, count, err := bridge(startup, *node, *runtimeDir, config.name, endpoint, config.env, config.args)
		if err != nil {
			return fmt.Errorf("%s: %w", config.name, err)
		}
		defer cleanup()
		total += count
		mux.Handle(endpoint, handler)
		log.Printf("Local reference %s: http://%s%s (%d tools)", config.name, listener.Addr(), endpoint, count)
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"local_test_only":true,"servers":2,"tools":%d}`, total)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("Test files: %s; memory: %s", files, filepath.Join(root, "memory.jsonl"))
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
