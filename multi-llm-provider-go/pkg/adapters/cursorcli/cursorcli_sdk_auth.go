package cursorcli

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

//go:embed cursor_sdk_auth.cjs
var cursorSDKAuthScript string

// SDKAuthCommand returns a fixed, credential-free command for the host's existing
// account UI. The caller must set the selected account's environment and confine
// personal account actions, granting ReadPaths in addition to that private home.
func SDKAuthCommand(ctx context.Context, action string) (command string, args, readPaths []string, err error) {
	switch action {
	case "login", "status", "logout", "models":
	default:
		return "", nil, nil, fmt.Errorf("unsupported Cursor account action")
	}
	if err = llmtypes.PrepareCodingSDK(ctx, "cursor-cli", nil); err != nil {
		return
	}
	var entry string
	entry, err = cursorSDKEntry()
	if err != nil {
		return
	}
	command, err = exec.LookPath("node")
	if err != nil {
		return
	}
	args = []string{"-e", cursorSDKAuthScript, entry, action}
	readPaths = []string{cursorSDKReadRoot(entry)}
	return
}

// The published package uses @cursor/sdk/dist/cjs/index.js. A custom module
// outside that layout gets only its own directory, never four arbitrary parents.
func cursorSDKReadRoot(entry string) string {
	if real, err := filepath.EvalSymlinks(entry); err == nil {
		entry = real
	}
	dir := filepath.Dir(entry)
	if filepath.Base(dir) == "cjs" && filepath.Base(filepath.Dir(dir)) == "dist" && filepath.Base(filepath.Dir(filepath.Dir(dir))) == "sdk" {
		parent := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
		if filepath.Base(parent) == "@cursor" {
			return parent
		}
	}
	return dir
}
