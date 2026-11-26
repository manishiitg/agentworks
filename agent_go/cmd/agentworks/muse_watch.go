package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/spf13/cobra"
)

const museMonitorSetup = "Muse AgentWorks delivery setup: call the agentworks tool (action=monitor) and start ONE native monitor with its returned arguments if monitor_active is false. Use the monitor tool, persistent=true, wake_delay_ms=0, show_lines=true. This consumer uses the already person-approved attachment; it cannot attach or approve. Load the agentworks-muse-monitor skill. Do not run a shell listener or a polling loop."

// Trusted provider setup never includes peer-written roster/purpose text.
func museAttachSetup(out agentworksclient.LocalBridgeReply) string {
	return fmt.Sprintf("AgentWorks Muse setup: person attach completed. This exact native session %q is attached as agent:%q. All account senders are accepted.\n%s\nSet your own purpose with the agentworks tool (action=set_purpose) and fetch peers with the agentworks tool (action=peers) as tool output. Never repeat attachment or trust changes through a tool.", out.Attachment.SessionID, out.Attachment.AgentName, museMonitorSetup)
}

func museWatchCommand(o *options) *cobra.Command {
	var provider, session, lease, subscription string
	var after int
	cmd := &cobra.Command{Use: "watch", Args: cobra.NoArgs, Short: "Wait for messages in this attached Codex session or Muse Monitor", RunE: func(cmd *cobra.Command, _ []string) error {
		if provider == "codex" {
			if session != "" || lease != "" || subscription != "" {
				return errors.New("Codex watch detects this exact session; do not supply session or lease overrides")
			}
			return codexTeamWatch(cmd.Context(), o, after)
		}
		if provider != "muse" {
			return errors.New("watch supports Codex or the Muse native Monitor")
		}
		if session == "" || lease == "" || subscription == "" {
			return errors.New("use the exact Monitor command returned by the agentworks tool (action=monitor)")
		}
		record, err := agentworksclient.ReadMuseMonitorSubscription(subscription)
		if err != nil {
			return err
		}
		if record.SessionID != session || record.AttachmentID != lease {
			return errors.New("Monitor command changed native session or lease")
		}
		if !museMonitorArtifactAt(session, record.DataHome) {
			return errors.New("arm this command with Muse's native monitor tool; bash cannot wake an idle chat")
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		encoder := json.NewEncoder(o.stdout)
		return agentworksclient.ReadMuseMonitorPipe(ctx, subscription, session, lease, func(hint agentworksclient.MuseWatchHint) error { return encoder.Encode(hint) })
	}}
	cmd.Flags().StringVar(&provider, "provider", "", "Attached provider (codex or muse)")
	cmd.Flags().StringVar(&session, "session", "", "Native-session pin returned by the Muse messaging MCP")
	cmd.Flags().StringVar(&lease, "attachment-id", "", "Approved-lease pin returned by the Muse messaging MCP")
	cmd.Flags().StringVar(&subscription, "subscription-file", "", "Private metadata subscription returned by the approved Muse MCP")
	cmd.Flags().IntVar(&after, "after", 0, "Codex mailbox position from the previous read (one bounded wait, no polling loop)")
	return cmd
}

// Muse gives both bash and Monitor children a tool-use ID. Its pre-opened
// monitor stdout spool distinguishes the tools, confined to the observed UUID.
// This prevents accidental bash listeners, not same-OS-user runtime forgery.
func museMonitorArtifact(session string) bool {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		data = filepath.Join(home, ".local", "share")
	}
	return museMonitorArtifactAt(session, data)
}

func museMonitorArtifactAt(session, data string) bool {
	call := os.Getenv("MUSE_TOOL_USE_ID")
	if len(call) > 128 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(call) {
		return false
	}
	root := filepath.Join(data, "muse", "sessions")
	// Native dated store and the earlier flat layout. The exact UUID comes from
	// the live hook/process fence, never from a model-selected directory scan.
	for _, directory := range []string{filepath.Join(root, "*", "*", "*", session), filepath.Join(root, session)} {
		for _, tail := range []string{filepath.Join("tool-outputs", ".spool", call+"-monitor-stdout.txt.tmp"), filepath.Join("tool-outputs", "*", call+"-monitor-stdout.txt")} {
			paths, _ := filepath.Glob(filepath.Join(directory, tail))
			for _, path := range paths {
				info, err := os.Lstat(path)
				resolved, e := filepath.EvalSymlinks(path)
				if err == nil && e == nil && info.Mode().IsRegular() && resolved == path {
					return true
				}
			}
		}
	}
	return false
}

// One server long-poll for this caller, not a new listener or native wake engine.
// Hook delivery still owns admission/processing receipts at turn boundaries.
func codexTeamWatch(ctx context.Context, o *options, after int) error {
	if after < 0 {
		return errors.New("after must be non-negative")
	}
	session := os.Getenv("CODEX_THREAD_ID")
	if session == "" {
		return errors.New("Codex watch requires this tool call's CODEX_THREAD_ID; it cannot choose another chat")
	}
	host, err := providerHostKey("codex")
	if err != nil {
		return err
	}
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := agentworksclient.CallMessagingBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "read", Provider: "codex", SessionID: session, HostKey: host, Cursor: after, WaitSeconds: 20}, func(ctx context.Context) error { return ensureLocalBridge(ctx, root, o) })
	if err != nil {
		return err
	}
	if out.Error != "" {
		return errors.New(out.Error)
	}
	if !out.Attached {
		return errors.New("this exact Codex session is not attached")
	}
	if len(out.Result) == 0 {
		return errors.New("AgentWorks returned no inbox result")
	}
	_, err = fmt.Fprintln(o.stdout, string(out.Result))
	return err
}
