package codingagentmcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentjob"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxcapture"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type fakeJobService struct {
	startRequest codingagentjob.StartRequest
	startView    codingagentjob.View
	getView      codingagentjob.View
	cancelView   codingagentjob.View
	getError     error
	providers    []llmproviders.CodingAgentProviderContract
}

func (f *fakeJobService) Start(_ context.Context, request codingagentjob.StartRequest) (codingagentjob.View, error) {
	f.startRequest = request
	return f.startView, nil
}

func (f *fakeJobService) Get(_ context.Context, _ string) (codingagentjob.View, error) {
	return f.getView, f.getError
}

func (f *fakeJobService) Cancel(_ context.Context, _ string) (codingagentjob.View, error) {
	return f.cancelView, nil
}

func (f *fakeJobService) Providers() []llmproviders.CodingAgentProviderContract {
	return f.providers
}

func TestServerRegistersPollingTools(t *testing.T) {
	instance, err := New(&fakeJobService{}, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tools := instance.Protocol().ListTools()
	for _, name := range []string{ToolListProviders, ToolListModels, ToolDelegate, ToolGetJob, ToolCancelJob} {
		if tools[name] == nil {
			t.Fatalf("tool %q is not registered", name)
		}
	}
	if len(tools) != 5 {
		t.Fatalf("registered tools = %d, want 5", len(tools))
	}
	if _, ok := tools[ToolGetJob].Tool.InputSchema.Properties["include_terminal_output"]; !ok {
		t.Fatal("get_coding_agent_job is missing include_terminal_output")
	}
}

func TestHandleDelegateStartsBackgroundJob(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	service := &fakeJobService{startView: codingagentjob.View{
		JobID:     "job_123",
		Provider:  "cursor-cli",
		Status:    codingagentjob.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
		PollAfter: 15,
		NextTool:  ToolGetJob,
	}}
	instance, err := New(service, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := instance.handleDelegate(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"provider":        "cursor-cli",
			"task":            "review authentication",
			"working_dir":     "/workspace",
			"model":           "auto",
			"timeout_seconds": float64(1800),
		}},
	})
	if err != nil {
		t.Fatalf("handleDelegate() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleDelegate() result = %#v", result)
	}
	if service.startRequest.Provider != "cursor-cli" || service.startRequest.TimeoutSeconds != 1800 {
		t.Fatalf("Start request = %#v", service.startRequest)
	}
	view, ok := result.StructuredContent.(codingagentjob.View)
	if !ok || view.JobID != "job_123" || view.NextTool != ToolGetJob {
		t.Fatalf("StructuredContent = %#v", result.StructuredContent)
	}
}

func TestHandleGetJobReturnsNotFoundAsToolError(t *testing.T) {
	service := &fakeJobService{getError: codingagentjob.ErrJobNotFound}
	instance, err := New(service, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := instance.handleGetJob(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"job_id": "job_missing"}},
	})
	if err != nil {
		t.Fatalf("handleGetJob() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("handleGetJob() IsError = false")
	}
}

func TestHandleGetJobCanIncludeSharedTmuxTerminalOutput(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	service := &fakeJobService{getView: codingagentjob.View{
		JobID:       "job_running",
		Provider:    "cursor-cli",
		Status:      codingagentjob.StatusRunning,
		TmuxSession: "cursor-pane",
		CreatedAt:   now,
		UpdatedAt:   now,
	}}
	instance, err := New(service, "test")
	if err != nil {
		t.Fatal(err)
	}
	captureCalls := 0
	instance.captureTerminal = func(_ context.Context, session string) (tmuxcapture.Snapshot, error) {
		captureCalls++
		if session != "cursor-pane" {
			t.Fatalf("capture session = %q", session)
		}
		return tmuxcapture.Snapshot{Text: "Running tests", Truncated: true, CapturedAt: now}, nil
	}
	result, err := instance.handleGetJob(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"job_id":                  "job_running",
			"include_terminal_output": true,
		}},
	})
	if err != nil || result.IsError {
		t.Fatalf("handleGetJob() result=%#v err=%v", result, err)
	}
	view, ok := result.StructuredContent.(codingagentjob.View)
	if !ok || view.TerminalOutput != "Running tests" || !view.TerminalTruncated || view.TerminalCapturedAt == nil {
		t.Fatalf("terminal view = %#v", result.StructuredContent)
	}
	if captureCalls != 1 {
		t.Fatalf("capture calls = %d", captureCalls)
	}
}

func TestHandleGetJobKeepsNormalPollingLightweight(t *testing.T) {
	service := &fakeJobService{getView: codingagentjob.View{
		JobID:       "job_running",
		Status:      codingagentjob.StatusRunning,
		TmuxSession: "cursor-pane",
	}}
	instance, err := New(service, "test")
	if err != nil {
		t.Fatal(err)
	}
	instance.captureTerminal = func(_ context.Context, _ string) (tmuxcapture.Snapshot, error) {
		t.Fatal("lightweight polling captured tmux output")
		return tmuxcapture.Snapshot{}, nil
	}
	result, err := instance.handleGetJob(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"job_id": "job_running"}},
	})
	if err != nil || result.IsError {
		t.Fatalf("handleGetJob() result=%#v err=%v", result, err)
	}
}

func TestHandleGetJobReportsTmuxCaptureFailureWithoutLosingStatus(t *testing.T) {
	service := &fakeJobService{getView: codingagentjob.View{
		JobID:       "job_running",
		Status:      codingagentjob.StatusRunning,
		TmuxSession: "missing-pane",
	}}
	instance, err := New(service, "test")
	if err != nil {
		t.Fatal(err)
	}
	instance.captureTerminal = func(_ context.Context, _ string) (tmuxcapture.Snapshot, error) {
		return tmuxcapture.Snapshot{}, errors.New("tmux session disappeared")
	}
	result, err := instance.handleGetJob(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"job_id":                  "job_running",
			"include_terminal_output": true,
		}},
	})
	if err != nil || result.IsError {
		t.Fatalf("handleGetJob() result=%#v err=%v", result, err)
	}
	view := result.StructuredContent.(codingagentjob.View)
	if view.Status != codingagentjob.StatusRunning || !strings.Contains(view.TerminalOutputError, "session disappeared") {
		t.Fatalf("capture failure view = %#v", view)
	}
}

func TestHandleListProvidersReturnsContractData(t *testing.T) {
	service := &fakeJobService{providers: []llmproviders.CodingAgentProviderContract{{
		Provider:    llmproviders.ProviderCodexCLI,
		DisplayName: "Codex CLI",
		CLIName:     "codex",
		Transport:   llmproviders.CodingAgentTransportTmux,
	}}}
	instance, err := New(service, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := instance.handleListProviders(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleListProviders() error = %v", err)
	}
	if result.IsError || result.StructuredContent == nil {
		t.Fatalf("handleListProviders() result = %#v", result)
	}
}

func TestHandleListModelsReturnsPiCatalog(t *testing.T) {
	instance, err := New(&fakeJobService{}, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := instance.handleListModels(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"provider": "pi-cli"}},
	})
	if err != nil {
		t.Fatalf("handleListModels() error = %v", err)
	}
	if result.IsError || result.StructuredContent == nil {
		t.Fatalf("handleListModels() result = %#v", result)
	}
}

func TestJobErrorPreservesUnexpectedFailure(t *testing.T) {
	result := jobError("get failed", errors.New("database unavailable"))
	if !result.IsError || len(result.Content) == 0 {
		t.Fatalf("jobError() = %#v", result)
	}
}

func TestInProcessMCPHandshakeAndToolList(t *testing.T) {
	instance, err := New(&fakeJobService{}, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	mcpClient, err := client.NewInProcessClient(instance.Protocol())
	if err != nil {
		t.Fatalf("NewInProcessClient() error = %v", err)
	}
	t.Cleanup(func() { _ = mcpClient.Close() })
	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, request); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) != 5 {
		t.Fatalf("ListTools() returned %d tools, want 5", len(tools.Tools))
	}
}
