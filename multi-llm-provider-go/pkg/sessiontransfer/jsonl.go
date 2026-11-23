package sessiontransfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
)

func ClaudeSlug(workspace string) string {
	// Node and the native CLI resolve cwd physically (notably /var on macOS).
	if physical, e := filepath.EvalSymlinks(workspace); e == nil {
		workspace = physical
	}
	var out strings.Builder
	for _, r := range filepath.Clean(workspace) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	return out.String()
}

type codexMeta struct {
	ID     string          `json:"id"`
	Cwd    string          `json:"cwd"`
	Source json.RawMessage `json:"source"`
}

func readCodexMeta(path string) (codexMeta, error) {
	f, e := os.Open(path)
	if e != nil {
		return codexMeta{}, e
	}
	defer f.Close()
	line, e := bufio.NewReader(io.LimitReader(f, 2<<20)).ReadBytes('\n')
	if e != nil {
		return codexMeta{}, e
	}
	var row struct {
		Type    string    `json:"type"`
		Payload codexMeta `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil || row.Type != "session_meta" || !UUID.MatchString(row.Payload.ID) {
		return codexMeta{}, fmt.Errorf("invalid Codex rollout metadata")
	}
	return row.Payload, nil
}

func codexParent(meta codexMeta) string {
	var source struct {
		Subagent struct {
			ThreadSpawn struct {
				Parent string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if json.Unmarshal(meta.Source, &source) != nil {
		return ""
	}
	return source.Subagent.ThreadSpawn.Parent
}

// ExportJSONL flattens only the selected conversation into private staging.
// Subagent journals keep native IDs; no home configuration or global index is
// part of the bundle. The caller closes its native writer before a handoff.
func ExportJSONL(provider, transport, store, workspace, id, out string) (*Bundle, error) {
	return ExportJSONLWithBudget(provider, transport, store, workspace, id, out, MaxBytes())
}

// ExportJSONLWithBudget checks every resolved source before staging native history.
func ExportJSONLWithBudget(provider, transport, store, workspace, id, out string, maxBytes int64) (*Bundle, error) {
	if provider == "pi-cli" && !piID.MatchString(id) || provider != "pi-cli" && !UUID.MatchString(id) || !filepath.IsAbs(store) || !filepath.IsAbs(out) {
		return nil, fmt.Errorf("invalid native export identity")
	}
	b := &Bundle{Provider: provider, Transport: transport, SessionID: id, Workspace: workspace, Root: out, Files: map[string]File{}}
	sources := map[string]string{}
	switch provider {
	case "claude-code":
		b.Format = "claude-jsonl"
		project := "projects/" + ClaudeSlug(workspace)
		p, e := Path(store, project+"/"+id+".jsonl", false)
		if e != nil {
			return nil, e
		}
		sources["session.jsonl"] = p
		for _, kind := range []string{"subagents", "tool-results"} {
			base, e := PathDirectory(store, project+"/"+id+"/"+kind, false)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return nil, e
			}
			entries, e := os.ReadDir(base)
			if e != nil {
				return nil, e
			}
			for _, entry := range entries {
				name := kind + "/" + entry.Name()
				if entry.IsDir() || !b.allowed(name) {
					continue
				}
				p, e := Path(base, entry.Name(), false)
				if e != nil {
					return nil, e
				}
				sources[name] = p
			}
		}
	case "codex-cli":
		b.Format = "codex-rollout"
		matches, e := filepath.Glob(filepath.Join(store, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
		if e != nil || len(matches) != 1 {
			return nil, fmt.Errorf("the native Codex rollout is missing or ambiguous")
		}
		p := matches[0]
		rel, e := filepath.Rel(store, p)
		if e != nil {
			return nil, e
		}
		p, e = Path(store, filepath.ToSlash(rel), false)
		if e != nil {
			return nil, e
		}
		meta, e := readCodexMeta(p)
		if e != nil || meta.ID != id {
			return nil, fmt.Errorf("the Codex rollout belongs to another session")
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 5 {
			return nil, fmt.Errorf("invalid Codex rollout location")
		}
		b.Date = strings.Join(parts[1:4], "/")
		sources["session.jsonl"] = p
		// Child ownership is an exact native parent link, never a common cwd.
		all, e := filepath.Glob(filepath.Join(store, "sessions", "*", "*", "*", "rollout-*.jsonl"))
		if e != nil {
			return nil, e
		}
		children := map[string]struct{ path, parent string }{}
		for _, candidate := range all {
			rel, e := filepath.Rel(store, candidate)
			if e != nil {
				continue
			}
			safe, e := Path(store, filepath.ToSlash(rel), false)
			if e != nil {
				return nil, e
			}
			m, e := readCodexMeta(safe)
			if e != nil {
				continue
			}
			if parent := codexParent(m); parent != "" {
				children[m.ID] = struct{ path, parent string }{safe, parent}
			}
		}
		owned := map[string]bool{id: true}
		for changed := true; changed; {
			changed = false
			for child, item := range children {
				if !owned[child] && owned[item.parent] {
					owned[child] = true
					sources["children/"+child+".jsonl"] = item.path
					changed = true
				}
			}
		}
	case "pi-cli":
		b.Format = "pi-jsonl"
		rel, e := piFind(store, id)
		if e != nil {
			if os.IsNotExist(e) {
				return nil, fmt.Errorf("the native Pi session is missing")
			}
			return nil, e
		}
		p, e := Path(store, rel, false)
		if e != nil {
			return nil, e
		}
		h, e := readPiHeader(p)
		if e != nil {
			return nil, e
		}
		if h.ID != id {
			return nil, fmt.Errorf("the Pi session file belongs to another session")
		}
		// The real cwd is descriptive (Workspace); the bundled header carries a placeholder.
		b.Workspace = *h.Cwd
		sources["session.jsonl"] = p
	default:
		return nil, fmt.Errorf("unsupported native journal provider")
	}
	if e := checkExportBudget(sources, maxBytes); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(out, 0700); e != nil {
		return nil, e
	}
	for name, source := range sources {
		if !b.allowed(name) {
			return nil, fmt.Errorf("unsupported native journal path")
		}
		dest, e := Path(out, name, true)
		if e != nil {
			return nil, e
		}
		if provider == "pi-cli" {
			e = piCopyWithCwd(out, dest, source, piWorkspacePlaceholder, "")
		} else {
			e = Copy(out, dest, source)
		}
		if e != nil {
			return nil, e
		}
		f, e := Inspect(dest)
		if e != nil {
			return nil, e
		}
		b.Files[name] = f
	}
	if e := b.ValidateFiles(); e != nil {
		return nil, e
	}
	if e := b.ValidateJournals(); e != nil {
		return nil, e
	}
	return b, nil
}

// PathDirectory uses the same component checks as Path, including the leaf.
func PathDirectory(root, name string, create bool) (string, error) {
	if filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid native directory")
	}
	base, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid native directory")
		}
		base = filepath.Join(base, part)
		if create {
			if e = os.Mkdir(base, 0700); e != nil {
				if !os.IsExist(e) {
					return "", e
				}
			} else if e = slotfs.ShareTree(root, base); e != nil {
				return "", e
			}
		}
		info, e := os.Lstat(base)
		if e != nil {
			return "", e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("native directory contains a link")
		}
	}
	return base, nil
}

func (b *Bundle) ValidateJournals() error {
	if b.Format == agyFormat {
		return b.validateAgy()
	}
	parents := map[string]string{}
	for name := range b.Files {
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		p, e := Path(b.Root, name, false)
		if e != nil {
			return e
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		reader := bufio.NewReaderSize(f, 64<<10)
		seen := false
		pi := &piJournalCheck{}
		for {
			line, err := reader.ReadBytes('\n')
			if err == io.EOF && len(line) == 0 {
				break
			}
			if err != nil || !json.Valid(line) {
				f.Close()
				return fmt.Errorf("native journal is incomplete")
			}
			if b.Provider == "pi-cli" {
				if e := pi.line(line, b.SessionID); e != nil {
					f.Close()
					return e
				}
			}
			if b.Provider == "claude-code" {
				var row struct {
					Session string `json:"sessionId"`
				}
				if json.Unmarshal(line, &row) != nil {
					f.Close()
					return fmt.Errorf("invalid Claude journal")
				}
				if row.Session != "" {
					if row.Session != b.SessionID {
						f.Close()
						return fmt.Errorf("Claude journal belongs to another session")
					}
					seen = true
				}
			}
		}
		f.Close()
		if b.Provider == "claude-code" && name == "session.jsonl" && !seen {
			return fmt.Errorf("Claude session identity is missing")
		}
		if b.Provider == "codex-cli" {
			m, e := readCodexMeta(p)
			if e != nil {
				return e
			}
			expected := b.SessionID
			if name != "session.jsonl" {
				expected = strings.TrimSuffix(filepath.Base(name), ".jsonl")
			}
			if name != "session.jsonl" {
				parents[m.ID] = codexParent(m)
			}
			if m.ID != expected {
				return fmt.Errorf("Codex journal belongs to another session")
			}
		}
	}
	for child := range parents {
		seen := map[string]bool{}
		for at := child; at != b.SessionID; at = parents[at] {
			if at == "" || seen[at] {
				return fmt.Errorf("Codex child is not owned by this session")
			}
			seen[at] = true
		}
	}
	return nil
}

func ImportJSONL(store, workspace string, b *Bundle) error {
	if e := b.ValidateFiles(); e != nil {
		return e
	}
	if e := b.ValidateJournals(); e != nil {
		return e
	}
	// Validate the prior local copy before any replacement. A newly selected
	// account or workspace may have no copy; an existing one must be an ancestor.
	rootExists := false
	if b.Provider == "claude-code" {
		_, e := Path(store, "projects/"+ClaudeSlug(workspace)+"/"+b.SessionID+".jsonl", false)
		rootExists = e == nil
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	piCwd := ""
	if b.Provider == "pi-cli" {
		var e error
		if piCwd, e = piDestinationCwd(workspace); e != nil {
			return e
		}
		if _, e = piFind(store, b.SessionID); e == nil {
			rootExists = true
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	if b.Provider == "codex-cli" {
		matches, e := filepath.Glob(filepath.Join(store, "sessions", "*", "*", "*", "rollout-*-"+b.SessionID+".jsonl"))
		if e != nil {
			return e
		}
		if len(matches) > 1 {
			return fmt.Errorf("native Codex session is ambiguous")
		}
		rootExists = len(matches) == 1
	}
	if rootExists {
		tmp, e := os.MkdirTemp("", "native-previous-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		previous, e := ExportJSONL(b.Provider, b.Transport, store, workspace, b.SessionID, tmp)
		if e != nil {
			return e
		}
		if e = Ancestor(previous, b); e != nil {
			return e
		}
	}
	for name := range b.Files {
		var relative string
		switch b.Format {
		case "claude-jsonl":
			base := "projects/" + ClaudeSlug(workspace) + "/" + b.SessionID
			if name == "session.jsonl" {
				relative = base + ".jsonl"
			} else {
				relative = base + "/" + name
			}
		case "codex-rollout":
			id := b.SessionID
			if name != "session.jsonl" {
				id = strings.TrimSuffix(filepath.Base(name), ".jsonl")
			}
			relative = "sessions/" + b.Date + "/rollout-" + strings.ReplaceAll(b.Date, "/", "-") + "T00-00-00-" + id + ".jsonl"
			existing, e := filepath.Glob(filepath.Join(store, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
			if e != nil {
				return e
			}
			if len(existing) > 1 {
				return fmt.Errorf("native Codex session is ambiguous")
			}
			if len(existing) == 1 {
				rel, e := filepath.Rel(store, existing[0])
				if e != nil {
					return e
				}
				relative = filepath.ToSlash(rel)
			}
		case "pi-jsonl":
			// Keep the file where Pi (or an earlier import) put it; a new one gets Pi's own name.
			existing, e := piFind(store, b.SessionID)
			if e == nil {
				relative = existing
			} else if os.IsNotExist(e) {
				source, e := Path(b.Root, name, false)
				if e != nil {
					return e
				}
				h, e := readPiHeader(source)
				if e != nil {
					return e
				}
				relative = piFileName(h)
			} else {
				return e
			}
		default:
			return fmt.Errorf("unsupported native journal import")
		}
		p, e := Path(store, relative, true)
		if e != nil {
			return e
		}
		source, e := Path(b.Root, name, false)
		if e != nil {
			return e
		}
		if b.Format == "pi-jsonl" {
			e = piCopyWithCwd(store, p, source, piCwd, "")
		} else {
			e = Copy(store, p, source)
		}
		if e != nil {
			return e
		}
	}
	return nil
}
