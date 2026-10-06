package browser

import (
	"fmt"
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
