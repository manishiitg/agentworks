package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// A tool list over 128 KiB used to travel in one environment variable, which
// Linux rejects at exec (spawn E2BIG), leaving Builder chats with no tools.
// The real bridge must start and list every tool from MCP_TOOLS_FILE.
func TestBridgeStartsWithToolListOver128KiB(t *testing.T) {
	const toolCount = 300
	defs := make([]ToolDef, 0, toolCount)
	for i := 0; i < toolCount; i++ {
		defs = append(defs, ToolDef{
			Name:        fmt.Sprintf("tool_%03d", i),
			Description: strings.Repeat("d", 1024),
			Type:        "custom",
		})
	}
	body, _ := json.Marshal(defs)
	if len(body) <= 128*1024 {
		t.Fatalf("tool list is only %d bytes", len(body))
	}
	bridge, err := client.NewStdioMCPClient(buildLargeOutputE2EBridge(t), append(os.Environ(),
		"MCP_API_URL=http://127.0.0.1:1/unused", "MCP_API_TOKEN=test-token",
		"MCP_TOOLS_FILE="+writeBridgeToolsFileForTest(t, body)))
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := bridge.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "tools-file-test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	listed, err := bridge.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != toolCount {
		t.Fatalf("bridge listed %d tools, want %d", len(listed.Tools), toolCount)
	}
}
