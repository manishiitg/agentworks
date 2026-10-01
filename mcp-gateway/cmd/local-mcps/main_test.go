package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
)

// Opt-in integration test against the actual installed reference packages.
func TestLocalReferenceServers(t *testing.T) {
	base := os.Getenv("CAPLAYER_LOCAL_REFERENCE_TEST_URL")
	data := os.Getenv("CAPLAYER_LOCAL_REFERENCE_TEST_DATA")
	if base == "" || data == "" {
		t.Skip("set local reference URL and dedicated test data directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fs, err := upstream.DialWithOptions(ctx, base+"/filesystem/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	mem, err := upstream.DialWithOptions(ctx, base+"/memory/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	call := func(c *upstream.Client, name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		r, err := c.Call(ctx, name, args)
		if err != nil || r.IsError {
			t.Fatalf("%s failed: %+v %v", name, r, err)
		}
		return r
	}
	text := func(r *mcp.CallToolResult) string {
		var out string
		for _, c := range r.Content {
			if c, ok := c.(mcp.TextContent); ok {
				out += c.Text
			}
		}
		return out
	}
	f, err := os.CreateTemp(filepath.Join(data, "files"), "mcp-smoke-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer os.Remove(f.Name())
	call(fs, "write_file", map[string]any{"path": f.Name(), "content": "Local MCP read/write succeeded"})
	if got := text(call(fs, "read_text_file", map[string]any{"path": f.Name()})); got != "Local MCP read/write succeeded" {
		t.Fatalf("read result: %q", got)
	}
	outside := filepath.Join(data, "outside-test.txt")
	denied, err := fs.Call(ctx, "read_text_file", map[string]any{"path": outside})
	if err != nil || !denied.IsError || !strings.Contains(strings.ToLower(text(denied)), "outside") {
		t.Fatalf("outside allowed directory: %+v %v", denied, err)
	}
	entity := fmt.Sprintf("caplayer-smoke-%d", time.Now().UnixNano())
	call(mem, "create_entities", map[string]any{"entities": []any{map[string]any{"name": entity, "entityType": "local-test", "observations": []string{"Reference MCP bridge verified"}}}})
	defer call(mem, "delete_entities", map[string]any{"entityNames": []string{entity}})
	if got := text(call(mem, "search_nodes", map[string]any{"query": entity})); !strings.Contains(got, entity) {
		t.Fatalf("memory search: %q", got)
	}
}
