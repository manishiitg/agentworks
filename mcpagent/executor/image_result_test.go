package executor

import (
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMCPImageResultsPreserveValueAndPointerBlocks(t *testing.T) {
	first := mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: "first"}
	second := mcp.ImageContent{Type: "image", MIMEType: "image/jpeg", Data: "second"}
	result := mcp.NewToolResultText("6 frames (3x2 grid)")
	result.Content = append(result.Content, first, &second)
	if got := mcpResultImages(result); !reflect.DeepEqual(got, []mcp.ImageContent{first, second}) {
		t.Fatalf("images changed: %#v", got)
	}
	if got := ConvertMCPResultToString(result); got != "6 frames (3x2 grid)" {
		t.Fatalf("legacy text changed: %q", got)
	}
	result.Content = []mcp.Content{first}
	if got := ConvertMCPResultToString(result); got == "Tool execution completed (no output returned)" {
		t.Fatal("image-only output reported as empty")
	}
	if mcpResultImages(nil) != nil {
		t.Fatal("nil result has images")
	}
}
