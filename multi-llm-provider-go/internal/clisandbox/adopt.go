package clisandbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// adoptResumedSession copies the native session a confined launch resumes into the private home
// when only the unconfined home has it. A chat started before its CLI was confined kept its
// session in the server's (or account's) own home; the confined launch reads the private home,
// so `resume <id>` found nothing and the pane exited at once (excellence Muse, RTS Claude and
// Cursor, 2026-09-30). Only this session's own files are copied, never the rest of that home
// (other people's sessions, indexes); a private copy that exists is kept. Claude keeps its own
// adopter (its transcript folder is named after the working directory).
func adoptResumedSession(policy *llmtypes.CLISecurityPolicy, args []string) {
	if policy == nil {
		return
	}
	provider := strings.TrimSpace(policy.Provider)
	id := resumeSessionID(provider, args)
	if id == "" || strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, "-") || strings.ContainsAny(id, "*?[") {
		return
	}
	home := canonical(policy.PrivateHome)
	sourceHome := strings.TrimSpace(policy.CredentialHome)
	env := func(key string) string { return policy.CredentialEnv[key] }
	if sourceHome == "" {
		sourceHome, _ = os.UserHomeDir()
		env = os.Getenv
	}
	if home == "" || sourceHome == "" || canonical(sourceHome) == home {
		return
	}
	for _, pattern := range sessionPatterns(provider, id) {
		sourceRoot, targetRoot := sourceHome, home
		if pattern.envKey != "" {
			if root := strings.TrimSpace(env(pattern.envKey)); root != "" {
				sourceRoot = root
			} else {
				sourceRoot = filepath.Join(sourceHome, pattern.fallback)
			}
			targetRoot = filepath.Join(home, pattern.fallback)
		}
		matches, _ := filepath.Glob(filepath.Join(sourceRoot, pattern.glob))
		for _, from := range matches {
			rel, err := filepath.Rel(sourceRoot, from)
			if err != nil {
				continue
			}
			to := filepath.Join(targetRoot, rel)
			if pattern.target != "" {
				to = filepath.Join(home, pattern.target, filepath.Base(filepath.Dir(from)), filepath.Base(from))
			}
			if _, err := os.Stat(to); err == nil {
				continue
			}
			_ = copyTree(from, to, home)
		}
	}
}

// resumeSessionID is the native session a launch resumes, by each CLI's own syntax.
func resumeSessionID(provider string, args []string) string {
	flag := ""
	switch provider {
	case "muse-cli", "codex-cli":
		flag = "resume"
	case "cursor-cli":
		flag = "--resume"
	default:
		return ""
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] != flag {
			continue
		}
		if provider != "codex-cli" {
			return strings.TrimSpace(args[i+1])
		}
		// Codex's interactive argv is "resume [OPTIONS] SESSION_ID".
		// Profiles, model and config flags precede the ID; their values
		// are not native session IDs. Structured exec resume can put it first.
		for j := i + 1; j < len(args); j++ {
			arg := strings.TrimSpace(args[j])
			if arg == "--" {
				if j+1 < len(args) {
					return strings.TrimSpace(args[j+1])
				}
				return ""
			}
			switch arg {
			case "--profile", "-p", "--model", "-m", "--sandbox", "-s", "--config", "-c",
				"--ask-for-approval", "-a", "--cd", "-C", "--disable", "--enable",
				"--image", "-i", "--local-provider", "--add-dir":
				j++
				continue
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			return arg
		}
		return ""
	}
	return ""
}

type sessionPattern struct {
	// envKey names the account path variable that says where the CLI keeps this data (empty:
	// under the home at glob); fallback is that folder's place under a home.
	envKey   string
	fallback string
	glob     string
	// target, when set, is where the match goes under the private home, keeping its
	// <hash>/<id> tail (the source is somewhere Cursor no longer reads).
	target string
}

func sessionPatterns(provider, id string) []sessionPattern {
	switch provider {
	case "muse-cli":
		return []sessionPattern{
			{envKey: "XDG_DATA_HOME", fallback: ".local/share", glob: filepath.Join("muse", "sessions", "*", "*", "*", id)},
			{envKey: "XDG_DATA_HOME", fallback: ".local/share", glob: filepath.Join("muse", "sessions", ".msp-view-v1", id)},
		}
	case "codex-cli":
		return []sessionPattern{{envKey: "CODEX_HOME", fallback: ".codex", glob: filepath.Join("sessions", "*", "*", "*", "rollout-*-"+id+".jsonl")}}
	case "cursor-cli":
		// Cursor keeps chats under its XDG config folder (the account's own, or the server's),
		// else ~/.cursor/chats; the private home's Cursor reads .config/cursor/chats.
		return []sessionPattern{
			{envKey: "XDG_CONFIG_HOME", fallback: ".config", glob: filepath.Join("cursor", "chats", "*", id)},
			{glob: filepath.Join(".cursor", "chats", "*", id), target: filepath.Join(".config", "cursor", "chats")},
		}
	}
	return nil
}

func copyTree(from, to, hint string) error {
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyRegular(from, to, hint)
	}
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, slotfs.Mode(hint, 0o700))
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyRegular(path, dest, hint)
	})
}

func copyRegular(from, to, hint string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), slotfs.Mode(hint, 0o700)); err != nil {
		return err
	}
	return os.WriteFile(to, data, slotfs.Mode(hint, 0o600))
}
