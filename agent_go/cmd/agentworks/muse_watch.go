package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/spf13/cobra"
)

const museMonitorSetup = "Muse AgentWorks delivery setup: call agent_message_monitor and start ONE native monitor with its returned arguments if monitor_active is false. Use the monitor tool, persistent=true, wake_delay_ms=0, show_lines=true. This consumer uses the already person-approved attachment; it cannot attach or approve. Load the agentworks-muse-monitor skill. Do not run a shell listener or a polling loop."

// Trusted provider setup never includes peer-written roster/purpose text.
func museAttachSetup(out agentworksclient.LocalBridgeReply) string {
	return fmt.Sprintf("AgentWorks Muse setup: person attach completed. This exact native session %q is attached as agent:%q. All account senders are accepted.\n%s\nSet your own purpose with agent_message_set_purpose and fetch peers with agent_message_peers as tool output. Never repeat attachment or trust changes through a tool.", out.Attachment.SessionID, out.Attachment.AgentName, museMonitorSetup)
}

func museWatchCommand(o *options) *cobra.Command {
	var provider, session, lease, subscription string
	cmd := &cobra.Command{Use: "watch", Args: cobra.NoArgs, Short: "Block on AgentWorks hints under this attached Muse session's native Monitor", RunE: func(cmd *cobra.Command, _ []string) error {
		if provider != "muse" {
			return errors.New("watch supports only the Muse native Monitor")
		}
		if session == "" || lease == "" || subscription == "" {
			return errors.New("use the exact Monitor command returned by agent_message_monitor")
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
	cmd.Flags().StringVar(&provider, "provider", "", "Attached provider (muse)")
	cmd.Flags().StringVar(&session, "session", "", "Native-session pin returned by the Muse messaging MCP")
	cmd.Flags().StringVar(&lease, "attachment-id", "", "Approved-lease pin returned by the Muse messaging MCP")
	cmd.Flags().StringVar(&subscription, "subscription-file", "", "Private metadata subscription returned by the approved Muse MCP")
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
