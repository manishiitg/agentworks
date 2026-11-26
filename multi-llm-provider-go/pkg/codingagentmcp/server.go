package codingagentmcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentjob"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentmodels"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxcapture"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	ToolListProviders = "list_coding_agents"
	ToolListModels    = "list_coding_agent_models"
	ToolDelegate      = "delegate_coding_agent"
	ToolGetJob        = "get_coding_agent_job"
	ToolCancelJob     = "cancel_coding_agent_job"
)

const instructions = `Coding-agent delegations are asynchronous. delegate_coding_agent returns a job_id immediately. Use get_coding_agent_job with that ID until status is completed, failed, cancelled, or timed_out. When completed, the same status response contains the final result. For a running job, set include_terminal_output=true when the user asks what is happening or progress appears stale; the response then includes a bounded plain-text capture of the live tmux pane. While a tmux worker is active, status may also include tmux_capture_command for deeper non-blocking inspection and tmux_attach_command for a human terminal. Do not run tmux_attach_command from the host agent because attaching is interactive and blocks its terminal. Do not claim a delegation finished before retrieving its terminal status. For working_dir, use the current trusted project root automatically; do not ask the user to type a path. Use list_coding_agent_models before delegation when the user requests a specific model.`

const terminalCaptureTimeout = 5 * time.Second

type JobService interface {
	Start(context.Context, codingagentjob.StartRequest) (codingagentjob.View, error)
	Get(context.Context, string) (codingagentjob.View, error)
	Cancel(context.Context, string) (codingagentjob.View, error)
	Providers() []llmproviders.CodingAgentProviderContract
}

type Server struct {
	jobs            JobService
	protocol        *server.MCPServer
	captureTerminal func(context.Context, string) (tmuxcapture.Snapshot, error)
}

func New(jobs JobService, version string) (*Server, error) {
	if jobs == nil {
		return nil, fmt.Errorf("coding-agent job service is required")
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	instance := &Server{
		jobs: jobs,
		captureTerminal: func(ctx context.Context, tmuxSession string) (tmuxcapture.Snapshot, error) {
			captureCtx, cancel := context.WithTimeout(ctx, terminalCaptureTimeout)
			defer cancel()
			return (tmuxcapture.Capturer{}).CaptureAgentTail(captureCtx, tmuxSession, tmuxcapture.Options{})
		},
	}
	instance.protocol = server.NewMCPServer(
		"llm-provider-mcp",
		version,
		server.WithToolCapabilities(false),
		server.WithInstructions(instructions),
		server.WithRecovery(),
	)
	instance.registerTools()
	return instance, nil
}

func (s *Server) Protocol() *server.MCPServer {
	return s.protocol
}

func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.protocol)
}

func (s *Server) registerTools() {
	s.protocol.AddTool(mcp.NewTool(
		ToolListProviders,
		mcp.WithDescription("List coding-agent CLI providers that can receive delegated jobs."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	), s.handleListProviders)

	s.protocol.AddTool(mcp.NewTool(
		ToolListModels,
		mcp.WithDescription("List curated model selectors and dynamic model-discovery guidance for one coding-agent provider."),
		mcp.WithString("provider", mcp.Required(), mcp.Description("Provider ID returned by list_coding_agents.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	), s.handleListModels)

	s.protocol.AddTool(mcp.NewTool(
		ToolDelegate,
		mcp.WithDescription("Start an unattended coding-agent CLI job in the background. Returns immediately with a job_id; call get_coding_agent_job later for progress and the final result. Standard coding tools run without approval prompts in the trusted working_dir, using provider sandboxing where available."),
		mcp.WithString("provider", mcp.Required(), mcp.Description("Provider ID returned by list_coding_agents, such as codex-cli, cursor-cli, claude-code, or pi-cli.")),
		mcp.WithString("task", mcp.Required(), mcp.Description("Complete task for the delegated coding agent."), mcp.MaxLength(codingagentjob.MaxTaskBytes)),
		mcp.WithString("working_dir", mcp.Required(), mcp.Description("Current trusted project root. Infer this from the host session; do not ask the user to enter it.")),
		mcp.WithString("model", mcp.Description("Optional provider-specific model selector.")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Maximum job duration in seconds. Defaults to 2700 and cannot exceed 86400.")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
	), s.handleDelegate)

	s.protocol.AddTool(mcp.NewTool(
		ToolGetJob,
		mcp.WithDescription("Get a delegated coding-agent job's status and recent progress. Set include_terminal_output=true for a bounded plain-text snapshot of the active tmux pane when progress needs inspection. A completed response contains the final result. Respect poll_after_seconds before checking again."),
		mcp.WithString("job_id", mcp.Required(), mcp.Description("Opaque job ID returned by delegate_coding_agent.")),
		mcp.WithBoolean("include_terminal_output", mcp.Description("Capture the active tmux pane and include its latest plain-text output. Keep false for lightweight polling."), mcp.DefaultBool(false)),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	), s.handleGetJob)

	s.protocol.AddTool(mcp.NewTool(
		ToolCancelJob,
		mcp.WithDescription("Request cooperative cancellation of a queued or running coding-agent job."),
		mcp.WithString("job_id", mcp.Required(), mcp.Description("Opaque job ID returned by delegate_coding_agent.")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
	), s.handleCancelJob)
}

func (s *Server) handleListModels(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	provider, err := request.RequireString("provider")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	catalog, err := codingagentmodels.List(provider)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(catalog)
}

func (s *Server) handleListProviders(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type providerView struct {
		Provider    string `json:"provider"`
		DisplayName string `json:"display_name"`
		CLI         string `json:"cli"`
		Transport   string `json:"transport"`
	}
	contracts := s.jobs.Providers()
	providers := make([]providerView, 0, len(contracts))
	for _, contract := range contracts {
		providers = append(providers, providerView{
			Provider:    string(contract.Provider),
			DisplayName: contract.DisplayName,
			CLI:         contract.CLIName,
			Transport:   string(contract.Transport),
		})
	}
	return jsonResult(map[string]any{"providers": providers})
}

func (s *Server) handleDelegate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	provider, err := request.RequireString("provider")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := request.RequireString("task")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	workingDir, err := request.RequireString("working_dir")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	view, err := s.jobs.Start(ctx, codingagentjob.StartRequest{
		Provider:       provider,
		Model:          request.GetString("model", ""),
		Task:           task,
		WorkingDir:     workingDir,
		TimeoutSeconds: request.GetInt("timeout_seconds", 0),
	})
	if err != nil {
		return jobError("could not start coding-agent job", err), nil
	}
	return jsonResult(view)
}

func (s *Server) handleGetJob(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobID, err := request.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	view, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return jobError("could not get coding-agent job", err), nil
	}
	if request.GetBool("include_terminal_output", false) && view.TmuxSession != "" && !view.Status.Terminal() {
		snapshot, captureErr := s.captureTerminal(ctx, view.TmuxSession)
		if captureErr != nil {
			view.TerminalOutputError = captureErr.Error()
		} else {
			capturedAt := snapshot.CapturedAt
			view.TerminalOutput = snapshot.Text
			view.TerminalTruncated = snapshot.Truncated
			view.TerminalCapturedAt = &capturedAt
		}
	}
	return jsonResult(view)
}

func (s *Server) handleCancelJob(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobID, err := request.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	view, err := s.jobs.Cancel(ctx, jobID)
	if err != nil {
		return jobError("could not cancel coding-agent job", err), nil
	}
	return jsonResult(view)
}

func jsonResult(value any) (*mcp.CallToolResult, error) {
	result, err := mcp.NewToolResultJSON(value)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func jobError(prefix string, err error) *mcp.CallToolResult {
	if errors.Is(err, codingagentjob.ErrJobNotFound) {
		return mcp.NewToolResultError("coding-agent job was not found")
	}
	return mcp.NewToolResultErrorFromErr(prefix, err)
}

// TODO: Evaluate the io.modelcontextprotocol/tasks extension and task-status
// notifications after Codex, Claude Code, Cursor, and Pi client behavior is
// verified. Polling tools remain the compatibility baseline until then.
