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
	"maps"
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
	platformtools "github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"
	"github.com/spf13/cobra"
)

type options struct {
	serverURL, configPath string
	guidanceSessionID     string
	guidanceChecked       bool
	guidanceCachePath     string
	// switchPath is the person's chosen login for an attach that moves the chat.
	switchPath string
	// setupAll is set by agentworks setup: the person asked for the install, so
	// there is no extra yes prompt, and it installs for every project.
	setupAll bool
	// reconnectNote is set when a bare attach took back an earlier agent of the folder.
	reconnectNote string
	// newAgentWhy is set when a bare attach created a new agent although the person may have expected an earlier one back.
	newAgentWhy string
	// reconnectAlt are the other free earlier agents, tried when a bare attach loses a race for the first.
	reconnectAlt []autoAttachRecord
	reconnectCwd string
	// namedLogin is set by login --name so the save rechecks the name under a lock.
	namedLogin bool
	// attachedOnly limits live-chat discovery to chats already attached through
	// the bridge, which is all a move needs (and avoids probing every thread).
	attachedOnly   bool
	jsonOutput     bool
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
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
	{"schedules", "List, inspect, and trigger schedules", []struct{ command, tool string }{{"list", "project_schedules_list"}, {"runs", "project_schedules_runs"}, {"trigger", "project_schedules_run_now"}}},
	{"chat", "Send messages to the workflow assistant", []struct{ command, tool string }{{"ask", "chat"}}},
	{"messages", "Register named agents, discover addresses and manage your inbox", []struct{ command, tool string }{{"register", "messages"}, {"unregister", "messages"}, {"list", "messages"}, {"send", "messages"}, {"read", "messages"}, {"count", "messages"}, {"delete", "messages"}, {"clear", "messages"}}},
	{"guidance", "Load server-owned external guidance", []struct{ command, tool string }{{"context", "get_agent_context"}, {"topics", "list_guidance_topics"}, {"topic", "get_guidance_topic"}, {"skills", "get_guidance_skills"}}},
	{"knowledge", "Inspect workflow learnings, notes, and skills", []struct{ command, tool string }{{"list", "list_workflow_knowledge"}, {"read", "read_workflow_knowledge"}}},
	{"functions", "List, call and manage project functions", []struct{ command, tool string }{{"list", "project_functions_list"}, {"call", "project_functions_call"}, {"call-status", "project_functions_get_call"}, {"call-file", "project_functions_get_call"}, {"suggest", "suggest_workflow_change"}}},
	{"crews", "Discover, call, create, edit, export, and import Crews", []struct{ command, tool string }{{"list", "list_crews"}, {"get", "get_crew"}, {"files", "list_crew_files"}, {"search", "search_crew_files"}, {"read", "read_crew_file"}, {"ask", "ask_crew"}, {"suggest", "suggest_crew_change"}, {"create", "create_crew"}, {"update", "update_crew"}, {"put", "write_crew_file"}, {"export", "export_crew"}, {"import", "import_crew"}}},
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
				if apiErr.Request != nil {
					detail["request"] = apiErr.Request
				}
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
	root := &cobra.Command{Use: "agentworks", Version: cliVersion, Short: "Control a hosted AgentWorks server or expose its tools over MCP", SilenceUsage: true, SilenceErrors: true}
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) { o.checkGuidanceAtStart(cmd) }
	root.SetIn(o.stdin)
	root.SetOut(o.stdout)
	root.SetErr(o.stderr)
	root.AddCommand(openDesktopCommand())
	root.PersistentFlags().StringVar(&o.serverURL, "server", "", "Hosted AgentWorks HTTPS URL (or AGENTWORKS_SERVER)")
	root.PersistentFlags().StringVar(&o.configPath, "config", "", "Private connection config path")
	root.PersistentFlags().StringVar(&o.guidanceSessionID, "guidance-session", "", "Guidance receipt for this conversation (omit for explicit fresh preparation)")
	root.PersistentFlags().BoolVar(&o.jsonOutput, "json", false, "Emit compact JSON results and structured errors")
	root.AddCommand(loginCommand(o), loginsCommand(o), logoutCommand(o), skillsCommand(o), versionCommand(o), updateCommand(o), startCommand(o), stopCommand(o), statusCommand(o), watchCommand(o), debugCommand(o), serveShareCommand(o), pluginCommand(o), bridgeCommand(o), attachPluginCommand(o), claudeLauncherCommand(o), detachAgentCommand(o), agentsCommand(o), usageCommand(o), shareCommand(o, false), shareCommand(o, true), setupCommand(o))
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
	toolsCmd.AddCommand(&cobra.Command{Use: "spec NAME...", Args: cobra.MinimumNArgs(1), Short: "Prepare selected schemas and their current guidance", RunE: func(cmd *cobra.Command, names []string) error {
		client, err := o.client()
		if err != nil {
			return err
		}
		raw, err := client.Schemas(cmd.Context(), names)
		if err != nil {
			return err
		}
		return o.output(raw)
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
	root.AddCommand(publishCommand(o))
	root.AddCommand(brainCommand(o))
	root.AddCommand(secretCommand(o))
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
		Use: "plan get|list|edit", Args: cobra.MaximumNArgs(1),
		Short: "Read or edit a workflow plan with existing authoring consent",
		Long:  "Read with plan get. Discover native edit schemas with plan list. Use plan edit --input FILE with action, arguments, expected_revision from get_plan and a unique request_id. Identical retries return the saved receipt; current owner/editor/team roles and existing Builder consent apply.",
	}
	addOperationFlags(plan, "get_plan")
	plan.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		switch args[0] {
		case "get":
			return o.call(cmd, "get_plan")
		case "list":
			return o.callWithArguments(cmd, "plan", map[string]any{"action": "list"})
		case "edit":
			return o.call(cmd, "plan")
		default:
			return fmt.Errorf("unknown plan operation %q: use get, list or edit", args[0])
		}
	}
	root.AddCommand(plan)
	mcpCmd := &cobra.Command{Use: "mcp", Short: "Expose this hosted connection as MCP"}
	serveMCP := &cobra.Command{Use: "serve", Args: cobra.NoArgs, Short: "Serve MCP over stdio (stdout is reserved for protocol messages)", RunE: func(cmd *cobra.Command, _ []string) error {
		listenCtx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		client, err := o.client()
		if err != nil {
			return err
		}
		bridge, err := agentworksclient.NewMCPServer(cmd.Context(), client)
		if err != nil {
			return err
		}
		return agentworksclient.ServeMCPStdio(listenCtx, bridge, client, o.stdin, o.stdout)
	}}
	mcpCmd.AddCommand(serveMCP)
	root.AddCommand(mcpCmd)
	return root
}

func (o *options) path() (string, error) {
	if o.configPath != "" {
		return o.configPath, nil
	}
	if o.getenv != nil {
		if path := o.getenv("AGENTWORKS_CONFIG"); path != "" {
			return path, nil
		}
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
	if requested == "" && forLogin {
		requested = "https://network.agentworkshq.com"
	}
	if requested == "" {
		return cfg, path, errors.New("sign in first with agentworks login")
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
	o.configureGuidanceSession(client, cfg.Server, path)
	return client, nil
}

// saveLogin writes a login. A named login is saved under a lock that rechecks
// the name, because the browser sign-in takes a while and another sign-in could
// have taken the same name for a different server in the meantime.
func (o *options) saveLogin(path string, cfg agentworksclient.Config) error {
	if o.namedLogin {
		return agentworksclient.SaveLoginIfSameServer(path, cfg)
	}
	return agentworksclient.SaveConfig(path, cfg)
}

// loginPath resolves a login name (or "default") to its saved connection file and
// refuses names that have no saved login.
func (o *options) loginPath(name string) (string, error) {
	path, err := agentworksclient.LoginPath(name)
	if err != nil {
		return "", err
	}
	cfg, err := agentworksclient.LoadConfig(path)
	if err != nil {
		return "", err
	}
	if cfg.Server == "" {
		return "", fmt.Errorf("no login named %q; run agentworks login --server URL --name %s first", name, name)
	}
	return filepath.Abs(path)
}

func loginCommand(o *options) *cobra.Command {
	var loginName string
	var noBrowser bool
	var scopes []string
	cmd := &cobra.Command{Use: "login", Args: cobra.NoArgs, Short: "Sign in through your browser", Long: "Open AgentWorks in your browser, approve this CLI, and save a private renewable connection. Use --no-browser on a remote terminal and open the printed URL yourself. With --name the connection is saved under that name and never replaces the default login or another named login. Saving a named login does not connect any chats; add your chosen chat with agentworks attach --also NAME.", RunE: func(cmd *cobra.Command, _ []string) error {
		if loginName != "" {
			if o.configPath != "" || o.getenv("AGENTWORKS_CONFIG") != "" {
				return errors.New("--name cannot be combined with --config or AGENTWORKS_CONFIG")
			}
			if err := agentworksclient.ValidateLoginName(loginName); err != nil {
				return err
			}
			named, err := agentworksclient.LoginPath(loginName)
			if err != nil {
				return err
			}
			// A name that already signs in to another server is another login: never replace it.
			if existing, e := agentworksclient.LoadConfig(named); e != nil {
				return e
			} else if requested := firstNonEmpty(o.serverURL, o.getenv("AGENTWORKS_SERVER")); existing.Server != "" && requested != "" {
				want, e1 := agentworksclient.ValidateServer(requested)
				have, e2 := agentworksclient.ValidateServer(existing.Server)
				if e1 != nil || e2 != nil || want != have {
					return fmt.Errorf("the login name %q already signs in to %s; choose another name", loginName, existing.Server)
				}
			}
			o.configPath, o.namedLogin = named, true
		}
		cfg, path, err := o.connection(true)
		if err != nil {
			return err
		}
		if err := o.browserLogin(cmd.Context(), cfg, path, scopes, noBrowser); err != nil {
			return err
		}
		// A secondary sign-in is available for explicit per-chat joins. It
		// must not become the home network of other discovered native chats.
		if !o.namedLogin {
			connectLiveChats(cmd.Context(), o)
		}
		return o.output(map[string]any{"server": cfg.Server, "logged_in": true, "name": firstNonEmpty(loginName, agentworksclient.DefaultLoginName)})
	}}
	cmd.Flags().StringVar(&loginName, "name", "", "Save this login under a name (for example work) instead of replacing the default login")
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
			return o.saveLogin(path, cfg)
		}
		var apiErr *agentworksclient.APIError
		if !errors.As(err, &apiErr) || (apiErr.Code != "authorization_pending" && apiErr.Code != "slow_down") {
			if errors.As(err, &apiErr) && apiErr.Code == "invalid_target" {
				return errors.New("browser sign in: this server is not reached at the address you typed. Use the address it runs on (for a local install, http://127.0.0.1:PORT with the port of the app itself, not the page the dev tools open), or the address the app shows in its sign-in page")
			}
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
	var name string
	cmd := &cobra.Command{Use: "logout", Args: cobra.NoArgs, Short: "Revoke the browser connection and remove saved credentials", RunE: func(cmd *cobra.Command, _ []string) error {
		if name != "" {
			if o.configPath != "" || o.getenv("AGENTWORKS_CONFIG") != "" {
				return errors.New("--name cannot be combined with --config or AGENTWORKS_CONFIG")
			}
			named, err := agentworksclient.LoginPath(name)
			if err != nil {
				return err
			}
			o.configPath = named
		}
		path, err := o.path()
		if err != nil {
			return err
		}
		cfg, err := agentworksclient.LoadConfig(path)
		if err != nil {
			return err
		}
		onlyConfig := ""
		if name != "" {
			onlyConfig, err = filepath.Abs(path)
			if err != nil {
				return err
			}
			root, err := agentworksclient.DefaultLocalBridgeRoot()
			if err != nil {
				return err
			}
			if _, err = agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "ping"}); err == nil {
				out, err := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "sign_out_login", ConfigPath: onlyConfig})
				if err != nil || !out.SignedOut {
					return errors.New("Run agentworks update first, then sign out again.")
				}
			}
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
		if err := stopLaptopConnections(o, onlyConfig); err != nil {
			return err
		}
		if onlyConfig == "" {
			if err := stopAgentBridge(cmd.Context(), "sign_out"); err != nil {
				return err
			}
		}
		return o.output(map[string]any{"logged_out": true, "environment_token_active": o.getenv("AGENTWORKS_TOKEN") != ""})
	}}
	cmd.Flags().StringVar(&name, "name", "", "Sign out of this named login instead of the default login")
	return cmd
}

func addOperationFlags(cmd *cobra.Command, tool string) {
	f := cmd.Flags()
	f.String("input", "", "Tool arguments as a JSON object from a file, or - for stdin")
	// Global guidance topics accept no workflow_id; offering the flag would
	// only produce an avoidable invalid_arguments response.
	crewTool := strings.Contains(tool, "crew")
	if strings.HasPrefix(tool, "project_functions_") || strings.HasPrefix(tool, "project_schedules_") {
		f.String("project", "", "Project ID or name")
	}
	if tool != "messages" && tool != "list_workflows" && tool != "list_guidance_topics" && tool != "get_guidance_topic" && tool != "get_guidance_skills" && !crewTool && !(strings.HasPrefix(tool, "project_functions_") || strings.HasPrefix(tool, "project_schedules_")) {
		f.String("workflow", "", "Workflow ID (workflow_id)")
	}
	if crewTool && tool != "list_crews" && tool != "project_functions_get_call" && tool != "create_crew" && tool != "import_crew" {
		f.String("crew", "", "Crew ID from crews list (crew_id)")
	}
	switch tool {
	case "create_crew":
		f.String("idempotency-key", "", "One key per intended Crew; reuse it for an unchanged retry")

	case "project_functions_call":
		f.String("function", "", "Function name from functions list")
		f.String("function-input", "", "Function input as a JSON object")
	case "project_functions_get_call":
		f.String("call", "", "call_id returned by functions call")
	case "ask_crew":
		f.String("message", "", "Conversational message for the Crew; replies are optional")
		f.String("inbox", "", "Reuse the inbox_id returned by a previous send")
		f.String("submission-id", "", "Unique message submission ID; reuse after uncertain delivery")
	case "suggest_crew_change", "suggest_workflow_change":
		f.String("suggestion", "", "The change you would like, in plain words")
		f.String("reason", "", "Optional short reason or example")
		if tool == "suggest_crew_change" {
			f.String("about", "", "Optional function or schedule name it concerns")
		} else {
			f.String("step-id", "", "Optional related step ID")
		}
	}
	if tool == "project_functions_get_call" {
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
	if tool == "project_functions_call" {
		f.Int("wait", 0, "Seconds to wait for the result (max 25) before returning a call_id to poll")
	}
	if tool == "" {
		f.String("expected-revision", "", "Current revision from read_file/get_plan for guarded source or plan mutations")
	}
	f.StringArray("set", nil, "Set a native argument as key=JSON; repeatable (quote string JSON values)")
	if strings.Contains(tool, "file") || tool == "read_workflow_knowledge" {
		f.String("path", "", "Workspace-relative file or directory path")
	}
	if tool == "get_guidance_topic" {
		f.String("topic", "", "Guidance topic from guidance topics")
		f.String("known-version", "", "Saved topic version; unchanged bodies are omitted")
	}
	if tool == "list_workflows" || tool == "list_files" || tool == "search_files" || tool == "list_step_code" || tool == "list_runs" || tool == "get_run" || tool == "get_logs" || tool == "run_status" || tool == "project_schedules_runs" {
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
		f.String("agent", "", "Registered agent name: register/remove it, send as it, or read its incoming contacts")
		if cmd.Name() == "count" || cmd.Name() == "delete" || cmd.Name() == "clear" {
			f.String("id", "", "Incoming message ID to select")
			f.String("older-than", "", "Select messages older than an age, such as 24h or 7d")
			f.String("from", "", "Select messages from this registered agent name or ID")
			f.Bool("delivered", false, "Select only delivered messages")
			f.Bool("unread", false, "Select only messages with no acknowledgement")
		}
		if cmd.Name() == "register" {
			f.StringSlice("allow-connection", nil, "Explicit additional connection IDs of this account allowed to use this name; the registering connection is always included")
		}
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
	if tool == "project_schedules_runs" || tool == "project_schedules_run_now" {
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
	for flagName, field := range map[string]string{"project": "project", "workflow": "workflow_id", "crew": "crew_id", "function": "function", "call": "call_id", "expected-revision": "expected_revision", "path": "path", "query": "query", "glob": "glob", "run-folder": "run_folder", "session": "session_id", "message": "message", "suggestion": "suggestion", "about": "about", "provider": "provider", "model": "model_id", "step": "existing_step_id", "title": "title", "reason": "reason", "request-id": "request_id", "response": "response", "action": "action", "topic": "topic", "known-version": "known_version", "step-id": "step_id", "execution-id": "execution_id", "schedule-id": "schedule_id", "group": "group_name", "human-input": "human_input", "tier": "tier", "inbox": "inbox_id", "target": "target", "submission-id": "submission_id", "idempotency-key": "idempotency_key", "agent": "agent_name"} {
		if cmd.Flags().Changed(flagName) {
			value, _ := cmd.Flags().GetString(flagName)
			arguments[field] = value
		}
	}
	if cmd.Flags().Lookup("allow-connection") != nil && cmd.Flags().Changed("allow-connection") {
		arguments["allowed_connection_ids"], _ = cmd.Flags().GetStringSlice("allow-connection")
	}
	if cmd.Parent() != nil && cmd.Parent().Name() == "messages" {
		for flag, field := range map[string]string{"id": "message_id", "older-than": "older_than", "from": "from_agent"} {
			if cmd.Flags().Changed(flag) {
				arguments[field], _ = cmd.Flags().GetString(flag)
			}
		}
		for flag, field := range map[string]string{"delivered": "delivered_only", "unread": "unread_only"} {
			if cmd.Flags().Changed(flag) {
				arguments[field], _ = cmd.Flags().GetBool(flag)
			}
		}
	}
	if cmd.Flags().Lookup("function-input") != nil && cmd.Flags().Changed("function-input") {
		raw, _ := cmd.Flags().GetString("function-input")
		var object map[string]any
		if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
			return nil, errors.New("--function-input must be a JSON object, for example --function-input '{\"build\":\"812\"}'")
		}
		arguments["input"] = object
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
	if name == "messages" && cmd.Name() != "call" {
		arguments["action"] = cmd.Name()
		if cmd.Name() == "list" {
			arguments["action"] = "list_agents"
		}
		if cmd.Name() == "clear" {
			client, err := o.client()
			if err != nil {
				return err
			}
			preview := maps.Clone(arguments)
			preview["action"] = "count"
			raw, err := client.Call(cmd.Context(), "messages", preview)
			if err != nil {
				return err
			}
			var count struct {
				Matched *int `json:"matched"`
			}
			if err = json.Unmarshal(raw, &count); err != nil {
				return err
			}
			if count.Matched == nil || *count.Matched < 0 {
				return errors.New("The server did not return the matching inbox count. Try counting again.")
			}
			fmt.Fprintf(o.stderr, "%d matching messages. Clearing this agent's inbox selection...\n", *count.Matched)
		}
	}
	return o.callWithArguments(cmd, name, arguments)
}

func (o *options) callWithArguments(cmd *cobra.Command, name string, arguments map[string]any) error {
	if workflow, _ := cmd.Flags().GetString("workflow"); workflow != "" {
		arguments["workflow_id"] = workflow
	}
	client, err := o.client()
	if err != nil {
		return err
	}
	// Typed commands own their source identities. Explicit tools calls use the
	// server's current catalog verbatim, including refusal of retired names.
	typedCommand := cmd.Parent() == nil || cmd.Parent().Name() != "tools"
	if typedCommand {
		name, arguments = crewCommandCall(name, arguments)
		name, arguments = platformtools.Invocation(name, arguments)
	}
	result, err := client.Call(cmd.Context(), name, arguments)
	if err != nil {
		var failure *agentworksclient.APIError
		if typedCommand && errors.As(err, &failure) && (failure.Code == "unknown_tool" || failure.Status == 404 && failure.Code == "http_error") {
			friendly := *failure
			friendly.Message = "This command is unavailable on this server. Run agentworks update. If your CLI is current, ask the server administrator to update the server or check your access."
			return &friendly
		}
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

func loginsCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "logins", Args: cobra.NoArgs, Short: "List saved logins by name", Long: "Lists the default login and each named login with the server it signs in to. It never shows credentials or file paths.", RunE: func(cmd *cobra.Command, _ []string) error {
		logins, loadErr := agentworksclient.ListLogins()
		if o.jsonOutput {
			if err := o.output(map[string]any{"logins": logins}); err != nil {
				return err
			}
			return loadErr
		}
		if len(logins) == 0 {
			if loadErr != nil {
				return loadErr
			}
			_, err := fmt.Fprintln(o.stdout, "No saved logins. Run agentworks login --server URL [--name NAME].")
			return err
		}
		for _, l := range logins {
			fmt.Fprintf(o.stdout, "%s\t%s\n", l.Name, l.Server)
		}
		return loadErr
	}}
}

// Crew and workflow command arguments adapt to the same public families as MCP.
func crewCommandCall(name string, args map[string]any) (string, map[string]any) {
	family, action := "", ""
	switch name {
	case "list_crews":
		family, action = "workspace", "list"
	case "get_crew":
		family, action = "workspace", "get"
	case "ask_crew":
		family, action = "workspace", "ask"
	case "create_crew":
		family, action = "workspace", "create"
	case "update_crew":
		family, action = "workspace", "update"
	case "export_crew":
		family, action = "workspace", "export"
	case "import_crew":
		family, action = "workspace", "import"
	case "list_crew_files":
		family, action = "files", "list"
	case "search_crew_files":
		family, action = "files", "search"
	case "read_crew_file":
		family, action = "files", "read"
	case "write_crew_file":
		family, action = "files", "write"
	case "project_functions_list":
		family, action = "functions", "list"
	case "project_functions_call":
		family, action = "functions", "call"
	case "project_functions_get_call":
		family, action = "functions", "status"
	case "suggest_crew_change":
		family, action = "needs_you", "suggest"
	case "list_workflows":
		family, action = "workflow", "list"
	case "get_workflow":
		family, action = "workflow", "get"
	case "get_plan":
		family, action = "workflow", "plan"
	default:
		return name, args
	}
	out := map[string]any{}
	for k, v := range args {
		out[k] = v
	}
	out["action"] = action
	if family == "workspace" {
		if id, ok := out["crew_id"]; ok {
			out["workspace_id"] = id
			delete(out, "crew_id")
		}
		if action == "create" || action == "import" || action == "update" {
			settings := map[string]any{}
			for k, v := range out {
				if k != "workspace_id" && k != "action" && !(action == "create" && k == "idempotency_key") {
					settings[k] = v
					delete(out, k)
				}
			}
			key := "spec"
			if action == "update" {
				key = "changes"
			}
			out[key] = settings
			if action == "create" {
				out["kind"] = "crew"
			}
		}
	}
	return family, out
}
