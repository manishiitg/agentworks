// AgentWorks is a hosted API CLI and a stdio MCP bridge.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

type options struct {
	serverURL, configPath string
	jsonOutput            bool
	stdin                 io.Reader
	stdout, stderr        io.Writer
	getenv                func(string) string
}

// cliOperationGroups maps CLI subcommands to external tools. Every tool named
// here must stay admitted by product.yaml's run mode — external_tools or
// the proxied run.tools (see TestCLIOperationsStayAdmitted); `plan get` maps
// to get_plan the same way, while `tools call` and `mcp serve` resolve
// against the live server catalog instead. Structured run arguments
// (script_parameters, human_inputs, route_selections) travel via --set.
var cliOperationGroups = []struct {
	name, description string
	operations        []struct{ command, tool string }
}{
	{"workflows", "Discover workflows", []struct{ command, tool string }{{"list", "list_workflows"}, {"get", "get_workflow"}}},
	{"files", "Read and edit guarded workspace files", []struct{ command, tool string }{{"code", "list_step_code"}, {"link", "get_file_link"}, {"list", "list_files"}, {"read", "read_file"}, {"write", "write_file"}, {"search", "search_files"}}},
	{"runs", "Start, steer, stop, and inspect runs", []struct{ command, tool string }{{"list", "list_runs"}, {"get", "get_run"}, {"logs", "get_logs"}, {"start-step", "execute_step"}, {"start-workflow", "run_full_workflow"}, {"status", "run_status"}, {"message", "send_step_message"}, {"stop", "stop_step"}, {"stop-all", "stop_all_executions"}, {"executions", "list_executions"}, {"reply", "run_reply_input"}}},
	{"schedules", "List, inspect, and trigger schedules", []struct{ command, tool string }{{"list", "list_schedules"}, {"runs", "get_schedule_runs"}, {"trigger", "trigger_schedule"}}},
	{"chat", "Send messages to the workflow assistant", []struct{ command, tool string }{{"ask", "chat"}}},
	{"messages", "Send agent messages and read explicit replies from your inbox", []struct{ command, tool string }{{"send", "messages"}, {"read", "messages"}}},
	{"guidance", "Load server-owned external guidance", []struct{ command, tool string }{{"context", "get_agent_context"}, {"topics", "list_guidance_topics"}, {"topic", "get_guidance_topic"}}},
	{"knowledge", "Inspect workflow learnings, notes, and skills", []struct{ command, tool string }{{"list", "list_workflow_knowledge"}, {"read", "read_workflow_knowledge"}}},
	{"functions", "List and call a workflow's functions (typed, input-checked entry points)", []struct{ command, tool string }{{"list", "list_workflow_functions"}, {"call", "call_workflow_function"}, {"call-status", "get_workflow_function_call"}, {"call-file", "get_workflow_function_call"}, {"suggest", "suggest_workflow_change"}}},
	{"crews", "Discover, call, create, edit, export, and import Crews", []struct{ command, tool string }{{"list", "list_crews"}, {"get", "get_crew"}, {"files", "list_crew_files"}, {"search", "search_crew_files"}, {"read", "read_crew_file"}, {"functions", "list_crew_functions"}, {"call", "call_crew_function"}, {"ask", "ask_crew"}, {"call-status", "get_crew_function_call"}, {"call-file", "get_crew_function_call"}, {"suggest", "suggest_crew_change"}, {"create", "create_crew"}, {"update", "update_crew"}, {"put", "write_crew_file"}, {"export", "export_crew"}, {"import", "import_crew"}}},
}

func main() {
	if handled, code := localShellLauncher(os.Args[1:]); handled {
		os.Exit(code)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	o := &options{stdin: stdin, stdout: stdout, stderr: stderr, getenv: getenv}
	cmd := newCommand(o)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		code := 1
		var apiErr *agentworksclient.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.Status {
			case 401, 403:
				code = 3
			case 409:
				code = 4
			}
		}
		if o.jsonOutput {
			detail := map[string]any{"code": "client_error", "message": err.Error()}
			if apiErr != nil {
				detail = map[string]any{"code": apiErr.Code, "message": apiErr.Message, "status": apiErr.Status}
			}
			_ = json.NewEncoder(stderr).Encode(map[string]any{"error": detail})
		} else {
			fmt.Fprintln(stderr, "Error:", err)
		}
		return code
	}
	return 0
}

func newCommand(o *options) *cobra.Command {
	root := &cobra.Command{Use: "agentworks", Short: "Control a hosted AgentWorks server or expose its tools over MCP", SilenceUsage: true, SilenceErrors: true}
	root.SetIn(o.stdin)
	root.SetOut(o.stdout)
	root.SetErr(o.stderr)
	root.PersistentFlags().StringVar(&o.serverURL, "server", "", "Hosted AgentWorks HTTPS URL (or AGENTWORKS_SERVER)")
	root.PersistentFlags().StringVar(&o.configPath, "config", "", "Private connection config path")
	root.PersistentFlags().BoolVar(&o.jsonOutput, "json", false, "Emit compact JSON results and structured errors")
	root.AddCommand(loginCommand(o), logoutCommand(o), skillsCommand(o), versionCommand(o), updateCommand(o), executorCommand(o), startCommand(o), stopCommand(o), statusCommand(o), watchCommand(o), debugCommand(o), serveShareCommand(o))
	toolsCmd := &cobra.Command{Use: "tools", Short: "Discover and call the server's current tools"}
	toolsCmd.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List tools with authoritative JSON schemas", RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := o.client()
		if err != nil {
			return err
		}
		tools, err := client.Tools(cmd.Context())
		if err != nil {
			return err
		}
		return o.output(map[string]any{"tools": tools})
	}})
	call := &cobra.Command{Use: "call NAME", Args: cobra.MaximumNArgs(1), Short: "Call any server tool; --input accepts a JSON object file or stdin"}
	addOperationFlags(call, "")
	call.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return o.call(cmd, args[0])
	}
	toolsCmd.AddCommand(call)
	root.AddCommand(toolsCmd)
	for _, group := range cliOperationGroups {
		groupCmd := &cobra.Command{Use: group.name, Short: group.description}
		for _, op := range group.operations {
			toolName := op.tool
			cmd := &cobra.Command{Use: op.command, Args: cobra.NoArgs, Short: "Call " + toolName}
			addOperationFlags(cmd, toolName)
			cmd.RunE = func(cmd *cobra.Command, _ []string) error { return o.call(cmd, toolName) }
			groupCmd.AddCommand(cmd)
		}
		if group.name == "files" {
			download := &cobra.Command{Use: "download", Short: "Download an asset to a new local file (including large binary files)", Args: cobra.NoArgs}
			download.Flags().String("workflow", "", "Workflow ID")
			download.Flags().String("path", "", "Workflow-relative asset path")
			download.Flags().String("output", "", "New local destination file; existing files are never overwritten")
			download.RunE = func(cmd *cobra.Command, _ []string) error {
				workflow, _ := cmd.Flags().GetString("workflow")
				p, _ := cmd.Flags().GetString("path")
				output, _ := cmd.Flags().GetString("output")
				client, err := o.client()
				if err != nil {
					return err
				}
				size, err := client.Download(cmd.Context(), workflow, p, output)
				if err != nil {
					return err
				}
				absolute, _ := filepath.Abs(output)
				return o.output(map[string]any{"path": absolute, "size": size})
			}
			groupCmd.AddCommand(download)
		}
		root.AddCommand(groupCmd)
	}
	plan := &cobra.Command{
		Use: "plan get", Args: cobra.MaximumNArgs(1),
		Short: "Read a workflow plan and configuration",
		Long:  "Read with 'plan get'. Plan mutations are not exposed: tokens read and run, never author.",
	}
	addOperationFlags(plan, "get_plan")
	plan.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		if args[0] != "get" {
			return fmt.Errorf("unknown plan operation %q: v1 supports only 'plan get'", args[0])
		}
		return o.call(cmd, "get_plan")
	}
	root.AddCommand(plan)
	mcpCmd := &cobra.Command{Use: "mcp", Short: "Expose this hosted connection as MCP"}
	mcpCmd.AddCommand(&cobra.Command{Use: "serve", Args: cobra.NoArgs, Short: "Serve MCP over stdio (stdout is reserved for protocol messages)", RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := o.client()
		if err != nil {
			return err
		}
		bridge, err := agentworksclient.NewMCPServer(cmd.Context(), client)
		if err != nil {
			return err
		}
		return server.NewStdioServer(bridge).Listen(cmd.Context(), o.stdin, o.stdout)
	}})
	root.AddCommand(mcpCmd)
	return root
}

func (o *options) path() (string, error) {
	if o.configPath != "" {
		return o.configPath, nil
	}
	return agentworksclient.DefaultConfigPath()
}

func (o *options) connection(forLogin bool) (agentworksclient.Config, string, error) {
	path, err := o.path()
	if err != nil {
		return agentworksclient.Config{}, "", err
	}
	cfg, err := agentworksclient.LoadConfig(path)
	if err != nil {
		return cfg, path, err
	}
	requested := o.serverURL
	if requested == "" {
		requested = o.getenv("AGENTWORKS_SERVER")
	}
	if requested == "" {
		requested = cfg.Server
	}
	if requested == "" {
		return cfg, path, errors.New("server is required: use --server or agentworks login")
	}
	normalized, err := agentworksclient.ValidateServer(requested)
	if err != nil {
		return cfg, path, err
	}
	saved, _ := agentworksclient.ValidateServer(cfg.Server)
	// A command targeting another host must never reuse saved credentials.
	if saved != normalized {
		cfg.Token = ""
		cfg.RefreshToken = ""
		cfg.ExpiresAt = time.Time{}
	}
	cfg.Server = normalized
	if !forLogin {
		if token := o.getenv("AGENTWORKS_TOKEN"); token != "" {
			cfg.Token = token
			cfg.RefreshToken = ""
		}
	}
	return cfg, path, nil
}

func (o *options) client() (*agentworksclient.Client, error) {
	cfg, path, err := o.connection(false)
	if err != nil {
		return nil, err
	}
	client, err := agentworksclient.New(cfg.Server, cfg.Token)
	if err != nil {
		return nil, err
	}
	if cfg.RefreshToken != "" {
		client.WithTokenProvider(agentworksclient.ConfigTokenProvider(cfg.Server, path))
	}
	return client, nil
}

func loginCommand(o *options) *cobra.Command {
	var tokenStdin bool
	var noBrowser bool
	var scopes []string
	cmd := &cobra.Command{Use: "login", Args: cobra.NoArgs, Short: "Sign in through your browser", Long: "Open AgentWorks in your browser, approve this CLI, and save a private renewable connection. Use --no-browser on a remote terminal and open the printed URL yourself.", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, path, err := o.connection(true)
		if err != nil {
			return err
		}
		if !tokenStdin {
			if err := o.browserLogin(cmd.Context(), cfg, path, scopes, noBrowser); err != nil {
				return err
			}
			return o.output(map[string]any{"server": cfg.Server, "logged_in": true})
		}
		secret, err := io.ReadAll(io.LimitReader(o.stdin, 1025))
		if err != nil {
			return err
		}
		value := strings.TrimSpace(string(secret))
		if !strings.HasPrefix(value, "aw_pat_") || len(value) != 71 {
			return errors.New("expected a legacy AgentWorks personal access token (aw_pat_...)")
		}
		cfg.Token = value
		cfg.RefreshToken = ""
		cfg.ExpiresAt = time.Time{}
		client, err := agentworksclient.New(cfg.Server, cfg.Token)
		if err != nil {
			return err
		}
		if _, err := client.Tools(cmd.Context()); err != nil {
			return fmt.Errorf("verify token access: %w", err)
		}
		if err := agentworksclient.SaveConfig(path, cfg); err != nil {
			return err
		}
		return o.output(map[string]any{"server": cfg.Server, "logged_in": true})
	}}
	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Read a legacy personal access token from stdin")
	cmd.Flags().StringSliceVar(&scopes, "scopes", nil, "Explicit permissions for this connection (e.g. devices:connect for the local executor)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the approval link without opening a browser")
	return cmd
}

// browserLogin signs in through the browser (device authorization) and saves the renewable connection to path.
func (o *options) browserLogin(ctx context.Context, cfg agentworksclient.Config, path string, scopes []string, noBrowser bool) error {
	client, err := agentworksclient.New(cfg.Server, "")
	if err != nil {
		return err
	}
	device, err := client.StartDeviceAuthorization(ctx, scopes...)
	if err != nil {
		return fmt.Errorf("start browser sign in: %w", err)
	}
	fmt.Fprintln(o.stderr, "Open this link to sign in and approve AgentWorks CLI:")
	fmt.Fprintln(o.stderr, device.VerificationURIComplete)
	fmt.Fprintln(o.stderr, "Confirm the browser shows code:", device.UserCode)
	if !noBrowser {
		openLoginBrowser(device.VerificationURIComplete)
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(device.ExpiresIn)*time.Second)
	defer cancel()
	interval := time.Duration(device.Interval) * time.Second
	for {
		tokens, err := client.PollDeviceAuthorization(waitCtx, device.DeviceCode)
		if err == nil {
			cfg.Token = tokens.AccessToken
			cfg.RefreshToken = tokens.RefreshToken
			cfg.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
			return agentworksclient.SaveConfig(path, cfg)
		}
		var apiErr *agentworksclient.APIError
		if !errors.As(err, &apiErr) || (apiErr.Code != "authorization_pending" && apiErr.Code != "slow_down") {
			return fmt.Errorf("browser sign in: %w", err)
		}
		if apiErr.Code == "slow_down" {
			interval += 2 * time.Second
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("browser sign in expired: %w", waitCtx.Err())
		case <-time.After(interval):
		}
	}
}

func openLoginBrowser(link string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return
	}
	// Reap the opener in the background: it exits as soon as the browser
	// takes the link, and an unwaited child stays <defunct> (PLAT-150).
	go func() { _ = cmd.Wait() }()
}

func skillsCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "skills", Short: "Install the AgentWorks skill into a client skill directory"}
	var dir string
	var force bool
	install := &cobra.Command{Use: "install", Args: cobra.NoArgs, Short: "Install skills/agentworks/SKILL.md for Claude Code, Codex, and other skill clients", Long: "Copies the bundled AgentWorks skill (an entry pointer to the server-served guidance tools) into <dir>/agentworks/SKILL.md. Point --dir at a client skill directory, e.g. ~/.claude/skills or a project .claude/skills.", RunE: func(cmd *cobra.Command, _ []string) error {
		if dir == "" {
			dir = filepath.Join(".agents", "skills")
		}
		target, err := agentworksclient.InstallSkill(dir, force)
		if err != nil {
			return err
		}
		return o.output(map[string]any{"installed": target})
	}}
	install.Flags().StringVar(&dir, "dir", "", "Client skill directory (default .agents/skills)")
	install.Flags().BoolVar(&force, "force", false, "Overwrite an existing installed copy")
	cmd.AddCommand(install)
	return cmd
}

func logoutCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "logout", Args: cobra.NoArgs, Short: "Revoke the browser connection and remove saved credentials", RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := o.path()
		if err != nil {
			return err
		}
		cfg, err := agentworksclient.LoadConfig(path)
		if err != nil {
			return err
		}
		if cfg.Server != "" {
			if cfg.RefreshToken != "" {
				client, err := agentworksclient.New(cfg.Server, "")
				if err == nil {
					if revokeErr := client.RevokeOAuth(cmd.Context(), cfg.RefreshToken); revokeErr != nil {
						fmt.Fprintln(o.stderr, "Warning: server revocation failed; revoke the CLI connection in AgentWorks Connect.")
					}
				}
			}
			cfg.Token = ""
			cfg.RefreshToken = ""
			cfg.ExpiresAt = time.Time{}
			if err := agentworksclient.SaveConfig(path, cfg); err != nil {
				return err
			}
		}
		return o.output(map[string]any{"logged_out": true, "environment_token_active": o.getenv("AGENTWORKS_TOKEN") != ""})
	}}
}

func addOperationFlags(cmd *cobra.Command, tool string) {
	f := cmd.Flags()
	f.String("input", "", "Tool arguments as a JSON object from a file, or - for stdin")
	// Global guidance topics accept no workflow_id; offering the flag would
	// only produce an avoidable invalid_arguments response.
	crewTool := strings.Contains(tool, "crew")
	if tool != "messages" && tool != "list_workflows" && tool != "list_guidance_topics" && tool != "get_guidance_topic" && !crewTool {
		f.String("workflow", "", "Workflow ID (workflow_id)")
	}
	if crewTool && tool != "list_crews" && tool != "get_crew_function_call" && tool != "create_crew" && tool != "import_crew" {
		f.String("crew", "", "Crew ID from crews list (crew_id)")
	}
	switch tool {
	case "call_crew_function":
		f.String("function", "", "Function name from crews functions")
		f.String("args", "", "Function arguments as a JSON object")
	case "ask_crew":
		f.String("message", "", "Conversational message for the Crew; replies are optional")
		f.String("inbox", "", "Reuse the inbox_id returned by a previous send")
		f.String("submission-id", "", "Unique message submission ID; reuse after uncertain delivery")
	case "get_crew_function_call":
		f.String("call", "", "call_id returned by crews call")
	case "call_workflow_function":
		f.String("function", "", "Function name from functions list")
		f.String("args", "", "Function inputs as a JSON object")
	case "get_workflow_function_call":
		f.String("call", "", "call_id returned by functions call")
	case "suggest_crew_change", "suggest_workflow_change":
		f.String("suggestion", "", "The change you would like, in plain words")
		f.String("reason", "", "Optional short reason or example")
		if tool == "suggest_crew_change" {
			f.String("about", "", "Optional function or schedule name it concerns")
		} else {
			f.String("step-id", "", "Optional related step ID")
		}
	}
	if tool == "get_crew_function_call" || tool == "get_workflow_function_call" {
		f.Int("after", -1, "Execution message cursor; start at -1, then use next_after from the previous page")
		f.Int("after-event", -1, "Conversation text cursor; start at -1, then use next_after_event")
		f.Int("message-limit", 0, "Maximum execution messages and conversation text events to return")
		f.Int("wait", 0, "Seconds to wait for a terminal result, at most 25")
		f.String("file", "", "Read an output file listed on the call")
		f.Int("offset", 0, "Byte offset in the output file")
		f.Int("limit", 0, "Maximum output-file bytes to return")
	}
	if tool == "write_crew_file" {
		f.String("file", "", "Local file to upload: text or binary, at most 11 MiB. Read here by the CLI, so it is not pasted into the call")
	}
	if tool == "call_crew_function" || tool == "call_workflow_function" {
		f.Int("wait", 0, "Seconds to wait for the result (max 25) before returning a call_id to poll")
	}
	if tool == "" {
		f.String("expected-revision", "", "Revision from read_file/get_plan (reserved for a future write-enabled API)")
	}
	f.StringArray("set", nil, "Set a native argument as key=JSON; repeatable (quote string JSON values)")
	if strings.Contains(tool, "file") || tool == "read_workflow_knowledge" {
		f.String("path", "", "Workspace-relative file or directory path")
	}
	if tool == "get_guidance_topic" {
		f.String("topic", "", "Guidance topic from guidance topics")
	}
	if tool == "list_workflows" || tool == "list_files" || tool == "search_files" || tool == "list_step_code" || tool == "list_runs" || tool == "get_run" || tool == "get_logs" || tool == "run_status" || tool == "get_schedule_runs" {
		f.Int("limit", 0, "Maximum results")
		f.Int("offset", 0, "Result offset")
	}
	if tool == "list_files" || tool == "search_files" || tool == "list_crew_files" || tool == "search_crew_files" {
		f.Int("depth", 0, "Directory traversal depth (1..8)")
	}
	if tool == "list_files" || tool == "search_files" || tool == "list_step_code" || tool == "list_crew_files" || tool == "search_crew_files" {
		f.String("glob", "", "File path glob relative to the selected directory, e.g. '**/*.py'")
	}
	if tool == "list_step_code" {
		f.String("step-id", "", "Optional plan step ID to inventory")
	}
	if tool == "search_files" || tool == "list_workflows" || tool == "list_crews" || tool == "search_crew_files" {
		f.String("query", "", "Search query")
	}
	if tool == "get_run" || tool == "get_logs" {
		f.String("run-folder", "", "Run folder from list_runs")
	}
	if tool == "execute_step" {
		f.String("step-id", "", "Plan step ID or positional reference (e.g. '1')")
		f.String("group", "", "Variable group name")
		f.String("human-input", "", "Run-specific instructions or human_input response")
		f.String("tier", "", "LLM tier override: high, medium, or low")
	}
	if tool == "run_full_workflow" {
		f.String("group", "", "Variable group name to execute")
	}
	if tool == "run_status" || tool == "send_step_message" || tool == "stop_step" || tool == "stop_all_executions" || tool == "run_reply_input" {
		f.String("session", "", "Run session ID from a previous run call")
	}
	if tool == "run_status" {
		f.Int("since-index", -1, "Event index to poll from (-1 for the latest page)")
	}
	if tool == "send_step_message" || tool == "stop_step" {
		f.String("execution-id", "", "Execution ID from list_executions or run_status")
	}
	if tool == "send_step_message" {
		f.String("message", "", "Live correction for the running execution")
	}
	if tool == "chat" {
		f.String("message", "", "Conversational message for the workflow assistant; replies are optional")
		f.String("inbox", "", "Reuse the inbox_id returned by a previous send")
		f.String("submission-id", "", "Unique message submission ID; reuse after uncertain delivery")
	}
	if tool == "messages" {
		if cmd.Name() == "send" {
			f.String("target", "", "Authorized target agent name")
			f.String("crew", "", "Crew ID")
			f.String("workflow", "", "Workflow ID")
			f.String("message", "", "Message to send; no reply is required")
			f.String("submission-id", "", "Unique message submission ID; reuse after uncertain delivery")
		}
		f.String("inbox", "", "Inbox ID from a send acknowledgement; omit on first send to create one")
		if cmd.Name() == "read" {
			f.Int("after", 0, "Non-destructive message cursor; use next_cursor from the previous page")
			f.Int("limit", 0, "Maximum messages to read")
			f.Int("wait", 0, "Wait for new explicitly sent messages, at most 25 seconds; silence returns an empty page")
		}
	}
	if tool == "run_reply_input" {
		f.String("request-id", "", "Pending input request ID from runs status")
		f.String("response", "", "Answer to submit")
	}
	if tool == "get_schedule_runs" || tool == "trigger_schedule" {
		f.String("schedule-id", "", "Schedule ID from schedules list")
	}
}

// crewFileUploadMaxBytes and crewFileTextMaxBytes mirror the server: 11 MiB for a binary file, 2 MiB for text.
const (
	crewFileUploadMaxBytes = 11 << 20
	crewFileTextMaxBytes   = 2 << 20
)

func readInput(path string, stdin io.Reader) ([]byte, error) {
	var reader io.Reader = stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
	}
	data, err := io.ReadAll(io.LimitReader(reader, agentworksclient.MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > agentworksclient.MaxBodyBytes {
		return nil, errors.New("input exceeds 16 MiB limit")
	}
	return data, nil
}

func operationArguments(cmd *cobra.Command, stdin io.Reader) (map[string]any, error) {
	arguments := map[string]any{}
	input, _ := cmd.Flags().GetString("input")
	if input != "" {
		data, err := readInput(input, stdin)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&arguments); err != nil {
			return nil, fmt.Errorf("--input must contain a JSON object: %w", err)
		}
		if arguments == nil {
			return nil, errors.New("--input must contain a JSON object")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, errors.New("--input must contain exactly one JSON object")
		}
	}
	for flagName, field := range map[string]string{"workflow": "workflow_id", "crew": "crew_id", "function": "function", "call": "call_id", "expected-revision": "expected_revision", "path": "path", "query": "query", "glob": "glob", "run-folder": "run_folder", "session": "session_id", "message": "message", "suggestion": "suggestion", "about": "about", "provider": "provider", "model": "model_id", "step": "existing_step_id", "title": "title", "reason": "reason", "request-id": "request_id", "response": "response", "action": "action", "topic": "topic", "step-id": "step_id", "execution-id": "execution_id", "schedule-id": "schedule_id", "group": "group_name", "human-input": "human_input", "tier": "tier", "inbox": "inbox_id", "target": "target", "submission-id": "submission_id"} {
		if cmd.Flags().Changed(flagName) {
			value, _ := cmd.Flags().GetString(flagName)
			arguments[field] = value
		}
	}
	if cmd.Flags().Lookup("args") != nil && cmd.Flags().Changed("args") {
		raw, _ := cmd.Flags().GetString("args")
		var object map[string]any
		if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
			return nil, errors.New("--args must be a JSON object, for example --args '{\"build\":\"812\"}'")
		}
		arguments["args"] = object
	}
	for flagName, field := range map[string]string{"limit": "limit", "offset": "offset", "depth": "depth", "since-index": "since_index", "wait": "wait_seconds", "after": "after", "message-limit": "message_limit", "after-event": "after_event"} {
		if cmd.Flags().Changed(flagName) {
			value, _ := cmd.Flags().GetInt(flagName)
			arguments[field] = value
		}
	}
	if cmd.Flags().Lookup("file") != nil && cmd.Flags().Changed("file") && (cmd.Name() == "call-status" || cmd.Name() == "call-file") {
		arguments["file"], _ = cmd.Flags().GetString("file")
	}
	if cmd.Flags().Lookup("file") != nil && cmd.Flags().Changed("file") && cmd.Name() == "put" {
		localFile, _ := cmd.Flags().GetString("file")
		if input == "-" && localFile == "-" {
			return nil, errors.New("stdin can only supply one input")
		}
		data, err := readInput(localFile, stdin)
		if err != nil {
			return nil, err
		}
		if len(data) > crewFileUploadMaxBytes {
			return nil, fmt.Errorf("file exceeds the %d MiB upload limit", crewFileUploadMaxBytes>>20)
		}
		// Text goes as text, anything else (an image, PDF, spreadsheet) as base64.
		if utf8.Valid(data) && !bytes.Contains(data, []byte{0}) && len(data) <= crewFileTextMaxBytes {
			arguments["content"] = string(data)
		} else {
			arguments["content_base64"] = base64.StdEncoding.EncodeToString(data)
		}
	}
	for flagName, field := range map[string]string{"content-file": "content", "diff-file": "diff"} {
		if cmd.Flags().Changed(flagName) {
			path, _ := cmd.Flags().GetString(flagName)
			if input == "-" && path == "-" {
				return nil, errors.New("stdin can only supply one input")
			}
			data, err := readInput(path, stdin)
			if err != nil {
				return nil, err
			}
			arguments[field] = string(data)
		}
	}
	sets, _ := cmd.Flags().GetStringArray("set")
	for _, entry := range sets {
		key, raw, ok := strings.Cut(entry, "=")
		if !ok || key == "" || !json.Valid([]byte(raw)) {
			return nil, errors.New("--set requires key=JSON, for example --set 'title=\"New title\"'")
		}
		var value any
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		arguments[key] = value
	}
	return arguments, nil
}

func (o *options) call(cmd *cobra.Command, name string) error {
	arguments, err := operationArguments(cmd, o.stdin)
	if err != nil {
		return err
	}
	if name == "messages" && (cmd.Name() == "send" || cmd.Name() == "read") {
		arguments["action"] = cmd.Name()
	}
	client, err := o.client()
	if err != nil {
		return err
	}
	result, err := client.Call(cmd.Context(), name, arguments)
	if err != nil {
		return err
	}
	return o.output(result)
}

func (o *options) output(value any) error {
	encoder := json.NewEncoder(o.stdout)
	if !o.jsonOutput {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}
