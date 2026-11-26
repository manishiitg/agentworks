package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type frameMCPClient struct {
	mcpclient.ClientInterface
	frame mcp.ImageContent
}

func (c *frameMCPClient) Ping(context.Context) error { return nil }
func (c *frameMCPClient) Close() error               { return nil }
func (c *frameMCPClient) CallTool(_ context.Context, _ string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	result := mcp.NewToolResultText("6 frames (3x2 grid)")
	if args["image_only"] == true {
		result.Content = nil
	}
	result.Content = append(result.Content, c.frame)
	return result, nil
}

func testFrame(t *testing.T) mcp.ImageContent {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(data.Bytes())}
}

// Exercise the production executor and a real stdio mcpbridge process. The
// only fake is the upstream MCP service returning Jam-shaped text + frames.
func TestMCPBridgeImagesE2E(t *testing.T) {
	const session = "frame-bridge-test"
	frame := testFrame(t)
	registry := mcpclient.GetSessionRegistry()
	registry.StoreConnection(session, "jam", &frameMCPClient{frame: frame})
	t.Cleanup(func() { registry.CloseSession(session) })
	handler := executor.NewExecutorHandlers("missing-config", loggerv2.NewNoop())
	handler.SetMCPServerResolver(func(_ context.Context, sid, server, _ string) (*executor.ResolvedMCPServer, error) {
		if sid != session || server != "jam" {
			return nil, fmt.Errorf("server denied in current scope")
		}
		return &executor.ResolvedMCPServer{Name: "jam", ConnectionSessionID: session}, nil
	})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer frame-test" || r.Header.Get("X-Session-ID") != session || r.URL.Path != "/tools/mcp/jam/getFrames" {
			http.Error(w, "bad scoped request", http.StatusForbidden)
			return
		}
		handler.HandlePerToolMCPRequest(w, r, "jam", "getFrames")
	}))
	defer api.Close()
	outputDir := filepath.Join(t.TempDir(), "tool_output_folder")
	defs, _ := json.Marshal([]ToolDef{{Name: "getFrames", Server: "jam", Type: "mcp", InputSchema: json.RawMessage(`{"type":"object","properties":{"image_only":{"type":"boolean"}}}`)}})
	bridge, err := client.NewStdioMCPClient(buildLargeOutputE2EBridge(t), append(os.Environ(),
		"MCP_API_URL="+api.URL, "MCP_API_TOKEN=frame-test", "MCP_SESSION_ID="+session,
		"MCP_TOOL_OUTPUT_DIR="+outputDir, "MCP_TOOLS_FILE="+writeBridgeToolsFileForTest(t, defs)))
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := bridge.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "frame-test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	for _, imageOnly := range []bool{false, true} {
		request := mcp.CallToolRequest{}
		request.Params.Name = "getFrames"
		request.Params.Arguments = map[string]interface{}{"image_only": imageOnly}
		result, err := bridge.CallTool(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		imageCount := 0
		for _, part := range result.Content {
			switch part := part.(type) {
			case mcp.TextContent:
				text.WriteString(part.Text)
			case mcp.ImageContent:
				imageCount++
				if part.Data != frame.Data || part.MIMEType != frame.MIMEType {
					t.Fatal("image bytes or MIME type changed")
				}
			}
		}
		if imageCount != 1 || !strings.Contains(text.String(), "Image 1 saved to:") {
			t.Fatalf("image-only=%v count=%d text=%s", imageOnly, imageCount, text.String())
		}
		if !imageOnly && !strings.Contains(text.String(), "6 frames (3x2 grid)") {
			t.Fatal("lost accompanying text")
		}
	}
	files, err := os.ReadDir(outputDir)
	if err != nil || len(files) != 2 {
		t.Fatalf("saved files=%v err=%v", files, err)
	}
	want, _ := base64.StdEncoding.DecodeString(frame.Data)
	for _, entry := range files {
		path := filepath.Join(outputDir, entry.Name())
		// #nosec G304 -- entry comes from the test-owned output directory, not tool arguments.
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("saved frame differs: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("image file permissions: %v", err)
		}
	}
}

func TestBridgeLargeImageUsesExactFileWithoutOversizedInlinePayload(t *testing.T) {
	frame := testFrame(t)
	data, _ := base64.StdEncoding.DecodeString(frame.Data)
	// Valid PNG with trailing bytes remains an exact binary artifact.
	data = append(data, bytes.Repeat([]byte{0}, 600*1024)...)
	frame.Data = base64.StdEncoding.EncodeToString(data)
	dir := t.TempDir()
	result := bridgeResultWithImages("frames", []mcp.ImageContent{frame}, dir)
	wire, _ := json.Marshal(result)
	if len(wire) >= 1024*1024 {
		t.Fatal("oversized stdio image payload")
	}
	for _, part := range result.Content {
		if _, ok := part.(mcp.ImageContent); ok {
			t.Fatal("large image emitted inline")
		}
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("large image was lost")
	}
	// #nosec G304 -- dir and its generated filename belong to this test's TempDir.
	got, _ := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if !bytes.Equal(got, data) {
		t.Fatal("large image was truncated")
	}
}

func TestBridgeInvalidImagesAreReportedWithoutBreakingText(t *testing.T) {
	frame := testFrame(t)
	frame.MIMEType = "image/jpeg"
	for _, invalid := range []mcp.ImageContent{frame, {Data: "invalid!", MIMEType: "image/png"}, {Data: "", MIMEType: "../../image"}} {
		result := bridgeResultWithImages("real tool text", []mcp.ImageContent{invalid}, t.TempDir())
		wire, _ := json.Marshal(result)
		if !strings.Contains(string(wire), "real tool text") || !strings.Contains(string(wire), "could not be delivered") {
			t.Fatal("invalid image hid text or failed silently")
		}
		for _, part := range result.Content {
			if _, ok := part.(mcp.ImageContent); ok {
				t.Fatal("invalid image emitted inline")
			}
		}
	}
}

func TestBridgeImageStillDeliveredWhenFileCannotBeSaved(t *testing.T) {
	result := bridgeResultWithImages("frames", []mcp.ImageContent{testFrame(t)}, "")
	if len(result.Content) != 3 {
		t.Fatalf("lost image or failure notice: %#v", result.Content)
	}
	if _, ok := result.Content[1].(mcp.ImageContent); !ok {
		t.Fatal("valid inline image was lost")
	}
}
