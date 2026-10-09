package browser

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Keep recording on the existing granted tab. Cursor/contact-sheet options
// create extra bindings/artifacts and are deliberately outside this contract.
func validateExtensionRecording(args []string, active bool) error {
	if len(args) == 1 && args[0] == "stop" {
		if !active {
			return fmt.Errorf("RECORDING_NOT_ACTIVE: this connection has no recording to stop")
		}
		return nil
	}
	if len(args) < 2 || args[0] != "start" {
		return fmt.Errorf("Extension recording supports record start <workspace-path.webm|.mp4> [url] [--fps 1-60] and record stop")
	}
	if active {
		return fmt.Errorf("RECORDING_ALREADY_ACTIVE: stop the current take before starting another")
	}
	if ext := strings.ToLower(filepath.Ext(args[1])); ext != ".webm" && ext != ".mp4" {
		return fmt.Errorf("Recording requires an explicit .webm or .mp4 workspace output path")
	}
	urlSeen, fpsSeen := false, false
	for i := 2; i < len(args); i++ {
		if args[i] == "--fps" && !fpsSeen && i+1 < len(args) {
			i++
			fps, err := strconv.Atoi(args[i])
			if err != nil || fps < 1 || fps > 60 {
				return fmt.Errorf("Recording --fps must be between 1 and 60")
			}
			fpsSeen = true
		} else if !urlSeen && (strings.HasPrefix(args[i], "http://") || strings.HasPrefix(args[i], "https://") || args[i] == "about:blank") {
			urlSeen = true
		} else {
			return fmt.Errorf("Unsupported extension recording argument %q", args[i])
		}
	}
	return nil
}

// defaultRecordingFPS is used when a take does not ask for a frame rate. At
// agent-browser's 30 fps a .webm take on a 2-vCPU server (server A) encodes at about
// real time, so any other load made the encoder fall behind and the whole take
// was lost ("Recording encoder fell more than 500 ms behind capture", server A,
// 2026-10-07). 15 fps is enough for screen recordings.
const defaultRecordingFPS = "15"

// withDefaultRecordingFPS adds --fps to a record start that has none.
func withDefaultRecordingFPS(values []string) []string {
	if len(values) == 0 || values[0] != "start" {
		return values
	}
	for _, v := range values {
		if v == "--fps" {
			return values
		}
	}
	return append(append([]string(nil), values...), "--fps", defaultRecordingFPS)
}

// ReleaseExtensionConversation tears down only this chat's native relay runtime
// and unfinished recording. It never closes the user's Chrome process.
func ReleaseExtensionConversation(ctx context.Context, owner string) {
	for _, session := range browserrelay.Default.ReleaseConversation(ctx, owner, common.SandboxBrowserSession(owner)) {
		clearSessionTabScope(session)
		resetCDPSessionRuntime(session)
		key := browserArtifactLeaseKey(owner, session, "record")
		if lease, ok := getBrowserArtifactLease(key); ok && lease.Transfer != nil {
			_ = os.Remove(lease.Transfer.SourcePath)
		}
		deleteBrowserArtifactLease(key)
	}
}
