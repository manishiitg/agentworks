// Package toolguard is one pre-call check every tool call passes through,
// whichever way it arrives: the agent's own loop (sequential and parallel),
// the executor HTTP API that CLI coding agents and scripts call over the
// bridge, and the code-execution registry. The embedding application installs
// one Guard; with none installed every call runs as before.
//
// A Guard can let a call run or answer it with a stub result instead. It is
// how an application runs a step in a test mode in which calls with external
// effects are recorded instead of performed (AgentWorks PLAT-562).
package toolguard

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
)

// Kind is how a tool is provided.
type Kind string

const (
	KindMCP     Kind = "mcp"     // a tool of an MCP server connection
	KindCustom  Kind = "custom"  // a tool the application registered (executor function)
	KindVirtual Kind = "virtual" // a tool built into the agent or registered as virtual
)

// Call describes one tool call before it runs.
type Call struct {
	// SessionID is the calling session: the agent's MCP session in the loop,
	// the request's session on the bridge. It may be empty.
	SessionID string
	Kind      Kind
	// Server is the MCP server name for KindMCP; empty otherwise.
	Server string
	Tool   string
	Args   map[string]interface{}
	// Annotations returns the MCP tool's own annotations as the server lists
	// them right now. Set only for KindMCP. found is false when the server
	// does not list the tool.
	Annotations func(ctx context.Context) (annotations mcp.ToolAnnotation, found bool, err error)
}

// Decision is a Guard's answer. The zero value lets the call run.
type Decision struct {
	// Stub answers the call with Result instead of running it.
	Stub   bool
	Result string
	// IsError marks the stubbed result as a tool error.
	IsError bool
}

// Guard decides one call. It must be safe for concurrent use.
type Guard func(ctx context.Context, call Call) Decision

var installed atomic.Pointer[Guard]

// Set installs the process-wide guard; nil removes it.
func Set(g Guard) {
	if g == nil {
		installed.Store(nil)
		return
	}
	installed.Store(&g)
}

// Check runs the installed guard. A guard that panics stubs the call with an
// error: a broken guard must never let a call through that it meant to stop.
func Check(ctx context.Context, call Call) (decision Decision) {
	p := installed.Load()
	if p == nil {
		return Decision{}
	}
	defer func() {
		if r := recover(); r != nil {
			decision = Decision{Stub: true, IsError: true, Result: fmt.Sprintf("tool %q was not run: the tool guard failed (%v)", call.Tool, r)}
		}
	}()
	return (*p)(ctx, call)
}

// AsResult converts a stub decision into an MCP tool result.
func (d Decision) AsResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: d.IsError, Content: []mcp.Content{&mcp.TextContent{Text: d.Result}}}
}

// Lister is the part of an MCP client the annotation lookup needs.
type Lister interface {
	ListTools(ctx context.Context) ([]mcp.Tool, error)
}

// AnnotationsFrom returns a lookup of tool's annotations on a live client. It
// lists the server's tools on first use and remembers the list for the call.
func AnnotationsFrom(client Lister, tool string) func(ctx context.Context) (mcp.ToolAnnotation, bool, error) {
	if client == nil {
		return func(context.Context) (mcp.ToolAnnotation, bool, error) {
			return mcp.ToolAnnotation{}, false, fmt.Errorf("no MCP client for tool %q", tool)
		}
	}
	var once sync.Once
	var tools []mcp.Tool
	var listErr error
	return func(ctx context.Context) (mcp.ToolAnnotation, bool, error) {
		once.Do(func() { tools, listErr = client.ListTools(ctx) })
		if listErr != nil {
			return mcp.ToolAnnotation{}, false, listErr
		}
		for _, t := range tools {
			if t.Name == tool {
				return t.Annotations, true, nil
			}
		}
		return mcp.ToolAnnotation{}, false, nil
	}
}
