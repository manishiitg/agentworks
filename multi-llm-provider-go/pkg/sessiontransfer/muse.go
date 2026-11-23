package sessiontransfer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

func inspectMuseJournal(path string, root bool, id string) (File, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, "", err
	}
	defer f.Close()
	hash := sha256.New()
	reader := bufio.NewReaderSize(io.TeeReader(io.LimitReader(f, MaxBytes()+1), hash), 64<<10)
	var size int64
	workspace := ""
	for {
		line, e := reader.ReadBytes('\n')
		if e == io.EOF && len(line) == 0 {
			break
		}
		if e != nil || !json.Valid(line) {
			return File{}, "", fmt.Errorf("incomplete or invalid Muse journal")
		}
		size += int64(len(line))
		if size > MaxBytes() {
			return File{}, "", fmt.Errorf("Muse journal exceeds transfer capacity")
		}
		if root {
			var row struct {
				Stream        struct{ Kind, ID string }
				PayloadType   string `json:"payload_type"`
				PayloadSchema int    `json:"payload_schema_version"`
				Payload       struct {
					Record struct {
						Workspace string `json:"workspace_root"`
						Build     struct{ Semver string }
					}
				}
			}
			if err := json.Unmarshal(line, &row); err != nil {
				return File{}, "", err
			}
			if row.Stream.Kind == "session" && row.Stream.ID != id {
				return File{}, "", fmt.Errorf("Muse journal belongs to another session")
			}
			if row.PayloadType == "runtime.session.metadata" {
				// The format is the metadata's payload schema; older Muse also wrote its build version (any 1.x reads the
				// same journal), current Muse writes none.
				if semver := row.Payload.Record.Build.Semver; semver != "" && !strings.HasPrefix(semver, "1.") || row.PayloadSchema > 1 {
					return File{}, "", fmt.Errorf("Muse journal format (build %q, schema %d) is unsupported", semver, row.PayloadSchema)
				}
				workspace = row.Payload.Record.Workspace
			}
		}
	}
	if size == 0 || (root && workspace == "") {
		return File{}, "", fmt.Errorf("Muse journal metadata is missing")
	}
	return File{Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, workspace, nil
}

// ExportMuse selects the bound native journal and its subagent journals.
func ExportMuse(dataHome, id, workspace, transport string) (*Bundle, error) {
	if transport == "" {
		transport = "structured"
	}
	if !UUID.MatchString(id) {
		return nil, fmt.Errorf("invalid Muse session ID")
	}
	root := filepath.Join(dataHome, "muse", "sessions")
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", "*", id, "session.jsonl"))
	if err != nil || len(matches) != 1 {
		return nil, fmt.Errorf("Muse session journal is unavailable or ambiguous")
	}
	rel, err := filepath.Rel(root, matches[0])
	if err != nil {
		return nil, err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid Muse journal layout")
	}
	b := &Bundle{Provider: "muse-cli", Format: "muse-jsonl", Transport: transport, SessionID: id, Date: strings.Join(parts[:3], "/"), Files: map[string]File{}}
	sessionRoot := filepath.Dir(matches[0])
	b.Root = sessionRoot
	total := int64(0)
	err = filepath.WalkDir(sessionRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A folder Muse made for itself that this account may not read (approval-review, owned by the slot it
			// ran in) holds no journal: it is left out. Only a folder that may hold journals must be readable.
			if d != nil && d.IsDir() && errors.Is(walkErr, fs.ErrPermission) {
				if rel, e := filepath.Rel(sessionRoot, path); e == nil && rel != "." && rel != "subagent" && !strings.HasPrefix(filepath.ToSlash(rel), "subagent/") {
					return filepath.SkipDir
				}
			}
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Muse journal tree contains a link")
		}
		if d.IsDir() {
			return nil
		}
		name, e := filepath.Rel(sessionRoot, path)
		if e != nil {
			return e
		}
		name = filepath.ToSlash(name)
		if !b.allowed(name) {
			return nil
		}
		p, e := Path(dataHome, "muse/sessions/"+b.Date+"/"+id+"/"+name, false)
		if e != nil {
			return e
		}
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		total += info.Size()
		if total > MaxBytes() || len(b.Files) >= 4096 {
			return fmt.Errorf("Muse journal is too large")
		}
		var workspace string
		b.Files[name], workspace, e = inspectMuseJournal(p, name == "session.jsonl", id)
		if name == "session.jsonl" {
			b.Workspace = workspace
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	if err = b.Validate(); err != nil {
		return nil, err
	}
	if !pathidentity.Same(workspace, b.Workspace) && !MuseHandoffWorkspace(dataHome, id, workspace) {
		return nil, fmt.Errorf("Muse session is not bound to this workspace")
	}
	return b, nil
}

// ImportMuse preserves divergent copies. An existing journal must be
// a byte-for-byte ancestor; timestamps never decide which conversation wins.
func ImportMuse(dataHome, workspace string, b *Bundle) error {
	if b == nil || b.Provider != "muse-cli" || b.Format != "muse-jsonl" {
		return fmt.Errorf("invalid Muse session bundle")
	}
	if err := b.ValidateFiles(); err != nil {
		return err
	}
	destination, err := pathidentity.Resolve(workspace)
	if err != nil {
		return err
	}
	root := filepath.Join(dataHome, "muse", "sessions")
	matches, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", b.SessionID, "session.jsonl"))
	want := filepath.Join(root, filepath.FromSlash(b.Date), b.SessionID, "session.jsonl")
	for _, match := range matches {
		if filepath.Clean(match) != filepath.Clean(want) {
			return fmt.Errorf("Muse session already exists at a different journal location")
		}
	}
	lockPath, err := Path(dataHome, "muse/sessions/"+b.Date+"/"+b.SessionID+"/.session.lock", true)
	if err != nil {
		return err
	}
	unlock, err := lockMuseSession(dataHome, lockPath)
	if err != nil {
		return err
	}
	defer unlock()
	paths := map[string]string{}
	if _, err := os.Stat(filepath.Dir(want)); err == nil {
		if err = filepath.WalkDir(filepath.Dir(want), func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("Muse journal tree contains a link")
			}
			if d.IsDir() {
				return nil
			}
			name, e := filepath.Rel(filepath.Dir(want), path)
			if e != nil {
				return e
			}
			name = filepath.ToSlash(name)
			if b.allowed(name) {
				if _, exists := b.Files[name]; !exists {
					return fmt.Errorf("Muse session has additional local history: %w", ErrDiverged)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for name := range b.Files {
		p, e := Path(dataHome, "muse/sessions/"+b.Date+"/"+b.SessionID+"/"+name, true)
		if e != nil {
			return e
		}
		if _, statErr := os.Stat(p); statErr == nil {
			old, _, e := inspectMuseJournal(p, name == "session.jsonl", b.SessionID)
			if e != nil {
				return e
			}
			if e = Ancestor(&Bundle{Provider: b.Provider, Format: b.Format, SessionID: b.SessionID, Root: strings.TrimSuffix(p, string(filepath.Separator)+filepath.FromSlash(name)), Files: map[string]File{name: old}}, b); e != nil {
				return e
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		paths[name] = p
	}
	marker, err := Path(dataHome, "muse/sessions/"+b.Date+"/"+b.SessionID+"/agentworks-handoff.json", true)
	if err != nil {
		return err
	}
	for name, p := range paths {
		source, err := Path(b.Root, name, false)
		if err != nil {
			return err
		}
		if err := Copy(dataHome, p, source); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(struct {
		SessionID string `json:"session_id"`
		Workspace string `json:"workspace"`
	}{b.SessionID, destination})
	return Write(dataHome, marker, raw)
}

func MuseHandoffWorkspace(dataHome, id, workspace string) bool {
	path := museTransferSessionPath(dataHome, id)
	if path == "" {
		return false
	}
	root := filepath.Join(dataHome, "muse", "sessions")
	rel, err := filepath.Rel(root, filepath.Join(filepath.Dir(path), "agentworks-handoff.json"))
	if err != nil {
		return false
	}
	path, err = Path(dataHome, "muse/sessions/"+filepath.ToSlash(rel), false)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 16384 {
		return false
	}
	var marker struct {
		SessionID string `json:"session_id"`
		Workspace string `json:"workspace"`
	}
	return json.Unmarshal(raw, &marker) == nil && marker.SessionID == id && pathidentity.Same(marker.Workspace, workspace)
}

func museTransferSessionPath(dataHome, id string) string {
	if !UUID.MatchString(id) {
		return ""
	}
	matches, e := filepath.Glob(filepath.Join(dataHome, "muse", "sessions", "*", "*", "*", id, "session.jsonl"))
	if e != nil || len(matches) != 1 {
		return ""
	}
	return matches[0]
}
