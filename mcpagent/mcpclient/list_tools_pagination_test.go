package mcpclient

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestListAllToolsFollowsCursors(t *testing.T) {
	pages := map[mcp.Cursor]mcp.ListToolsResult{
		"":   {Tools: []mcp.Tool{{Name: "a"}}, PaginatedResult: mcp.PaginatedResult{NextCursor: "p2"}},
		"p2": {Tools: []mcp.Tool{{Name: "b"}}, PaginatedResult: mcp.PaginatedResult{NextCursor: "p3"}},
		"p3": {Tools: []mcp.Tool{{Name: "c"}}},
	}
	tools, err := listAllTools(context.Background(), func(_ context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
		page := pages[request.Params.Cursor]
		return &page, nil
	})
	if err != nil || len(tools) != 3 || tools[2].Name != "c" {
		t.Fatalf("tools = %v %v", tools, err)
	}
	// A server that never stops paginating is bounded.
	_, err = listAllTools(context.Background(), func(_ context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
		return &mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "x"}}, PaginatedResult: mcp.PaginatedResult{NextCursor: request.Params.Cursor + "x"}}, nil
	})
	if err == nil {
		t.Fatal("endless pagination was not bounded")
	}
}
