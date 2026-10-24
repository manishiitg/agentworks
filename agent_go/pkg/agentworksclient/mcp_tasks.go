package agentworksclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const MCPTasksExtension = "io.modelcontextprotocol/tasks"

type MCPTaskFamily struct {
	Poll     string `json:"poll"`
	Reply    string `json:"reply"`
	Cancel   string `json:"cancel,omitempty"`
	IDField  string `json:"id_field"`
	Workflow bool   `json:"workflow"`
}

func MCPTaskFamilyFor(name string) (MCPTaskFamily, bool) {
	switch name {
	case "builder_chat":
		name = "builder_status"
	}
	f, ok := elicitationFamilyFor(name)
	if name == "run_status" {
		return MCPTaskFamily{}, false
	}
	if !ok {
		return MCPTaskFamily{}, false
	}
	out := MCPTaskFamily{Poll: f.get, Reply: f.reply, IDField: f.idField, Workflow: f.workflow}
	if name == "builder_status" {
		out.Cancel = "builder_cancel"
	}
	return out, true
}

// MCPTaskService is implemented by the authenticated server client. The
// transport holds no execution authority or task records.
type MCPTaskService interface {
	MCPTaskCall(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func (c *Client) MCPTaskCall(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return c.request(ctx, http.MethodPost, "/api/external/v1/mcp/tasks", struct {
		Method            string          `json:"method"`
		Params            json.RawMessage `json:"params"`
		GuidanceSessionID string          `json:"guidance_session_id,omitempty"`
	}{method, params, c.guidanceID(ctx)}, true)
}

// MCPTaskRequest routes only current Tasks requests and supported job tools.
// Everything else remains with the MCP SDK, including legacy elicitation.
type MCPTaskRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type MCPTaskParams struct {
	Name      string                     `json:"name"`
	Arguments map[string]any             `json:"arguments"`
	Meta      map[string]json.RawMessage `json:"_meta"`
}

func DecodeMCPTaskParams(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(value)
}

func (p MCPTaskParams) ClientCapabilities() *mcp.ClientCapabilities {
	var caps mcp.ClientCapabilities
	if json.Unmarshal(p.Meta[mcp.MetaKeyClientCapabilities], &caps) != nil {
		return nil
	}
	return &caps
}

func DecodeMCPTaskRequest(body []byte) (MCPTaskRequest, bool) {
	var request MCPTaskRequest
	if json.Unmarshal(body, &request) != nil || request.JSONRPC != "2.0" || len(request.ID) == 0 || string(request.ID) == "null" {
		return request, false
	}
	switch request.Method {
	case "tasks/get", "tasks/update", "tasks/cancel":
		return request, true
	case "tools/call":
		var params MCPTaskParams
		if DecodeMCPTaskParams(request.Params, &params) != nil {
			return request, false
		}
		var caps struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		}
		var version string
		_ = json.Unmarshal(params.Meta[mcp.MetaKeyProtocolVersion], &version)
		if version < mcp.ProtocolVersion20260728 || json.Unmarshal(params.Meta[mcp.MetaKeyClientCapabilities], &caps) != nil || caps.Extensions[MCPTasksExtension] == nil {
			return request, false
		}
		name, args := params.Name, params.Arguments
		if name == "call_tool" {
			name, _ = args["name"].(string)
			args, _ = args["arguments"].(map[string]any)
		}
		return request, MCPTaskCandidate(name, args)
	}
	return request, false
}

// Candidates are a routing hint only. The backend resolves canonical actions,
// validates their schema and applies the existing tool's authorization.
func MCPTaskCandidate(name string, args map[string]any) bool {
	action, _ := args["action"].(string)
	switch name {
	case "functions":
		return action == "call" || action == "status"
	case "builder":
		return action == "chat" || action == "status"
	case "call_crew_function", "get_crew_function_call", "call_workflow_function", "get_workflow_function_call", "builder_chat", "builder_status":
		return true
	}
	return false
}

func MCPTaskResponse(request MCPTaskRequest, raw json.RawMessage, err error) []byte {
	response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	if err == nil {
		response["result"] = raw
	} else {
		code, message := -32603, "The task service could not be reached. Try again."
		var api *APIError
		if errors.As(err, &api) {
			message = api.Message
			if api.Status == 404 {
				code, message = -32602, "Task not found or no longer available."
			} else if api.Status < 500 {
				code = -32602
			}
		}
		response["error"] = map[string]any{"code": code, "message": message}
	}
	body, _ := json.Marshal(response)
	return append(body, '\n')
}

type mcpLockedWriter struct {
	sync.Mutex
	io.Writer
}

func (w *mcpLockedWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Writer.Write(p)
}

// ServeMCPStdio adds the Tasks methods to the SDK transport. The SDK continues
// handling all other messages and its server-to-client request exchange.
func ServeMCPStdio(ctx context.Context, sdk *server.MCPServer, tasks MCPTaskService, input io.Reader, output io.Writer) error {
	if tasks == nil {
		return server.NewStdioServer(sdk).Listen(ctx, input, output)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	out := &mcpLockedWriter{Writer: output}
	go func() {
		var pending sync.WaitGroup
		defer func() { pending.Wait(); _ = writer.Close() }()
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64<<10), MaxBodyBytes)
		for scanner.Scan() {
			body := append([]byte(nil), scanner.Bytes()...)
			if request, handled := DecodeMCPTaskRequest(body); handled {
				pending.Add(1)
				go func() {
					defer pending.Done()
					raw, err := tasks.MCPTaskCall(ctx, request.Method, request.Params)
					_, _ = out.Write(MCPTaskResponse(request, raw, err))
				}()
			} else if _, err := writer.Write(append(body, '\n')); err != nil {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = writer.CloseWithError(err)
		}
	}()
	return server.NewStdioServer(sdk).Listen(ctx, reader, out)
}
