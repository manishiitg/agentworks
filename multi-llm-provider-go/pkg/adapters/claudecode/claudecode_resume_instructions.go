package claudecode

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/instructions"
)

// Native Claude resumes retain their original appended instructions, including
// with Agent SDK systemPrompt. Restate changed host instructions once, without
// replacing the native conversation or modifying its historical transcript.
// Remember only after success: a failed launch must resend them on retry.
func claudeCurrentResumeInstructions(opts *llmtypes.CallOptions, cwd, id, system, prompt string, resumed bool) (string, func()) {
	noop := func() {}
	if strings.TrimSpace(system) == "" || strings.TrimSpace(prompt) == "" || id == "" {
		return prompt, noop
	}
	root := llmtypes.CLIArtifactDirectory(opts, cwd)
	relative := fmt.Sprintf(".claude/.mlp-current-instructions-%x.sha256", sha256.Sum256([]byte(id)))
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(system)))
	changed := true
	if marker, e := claudeInstructionMarker(root, relative, false); e == nil {
		if raw, e := os.ReadFile(marker); e == nil && strings.TrimSpace(string(raw)) == digest {
			changed = false
		}
	}
	if resumed && changed {
		log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=started reason=current_instructions_needed", claudeInteractiveSessionIDFromOptions(opts), id)
		prompt = resentInstructionsStart + "\n" + system + "\n\n" + resentInstructionsEnd + "\n" + prompt
	}
	return prompt, func() {
		saved := false
		defer func() {
			if !saved {
				log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=failed reason=delivery_marker_write_failed", claudeInteractiveSessionIDFromOptions(opts), id)
			} else if resumed && changed {
				log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=done reason=current_instructions_delivered", claudeInteractiveSessionIDFromOptions(opts), id)
			}
		}()
		marker, e := claudeInstructionMarker(root, relative, true)
		if e == nil {
			f, e := os.CreateTemp(filepath.Dir(marker), ".instructions-")
			if e != nil {
				return
			}
			defer os.Remove(f.Name())
			_, e = f.WriteString(digest + "\n")
			closeErr := f.Close()
			if e != nil || closeErr != nil {
				return
			}
			if e = slotfs.ShareTree(root, f.Name()); e == nil {
				saved = os.Rename(f.Name(), marker) == nil
			}
		}
	}
}

func claudeInstructionMarker(root, relative string, create bool) (string, error) {
	base, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	dir := filepath.Join(base, ".claude")
	if create {
		if e = os.Mkdir(dir, 0700); e == nil {
			if e = slotfs.ShareTree(root, dir); e != nil {
				return "", e
			}
		} else if !os.IsExist(e) {
			return "", e
		}
	}
	info, e := os.Lstat(dir)
	if e != nil {
		return "", e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("instruction state contains a link")
	}
	marker := filepath.Join(base, relative)
	info, e = os.Lstat(marker)
	if create && os.IsNotExist(e) {
		return marker, nil
	}
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("instruction state contains a link")
	}
	return marker, nil
}

// The tmux transport's changed instructions go at the top of the next message
// between the shared markers.
const (
	resentInstructionsStart        = instructions.FullStart
	resentInstructionsEnd          = instructions.MessageStart
	resentInstructionsChangedStart = instructions.ChangesStart
)
