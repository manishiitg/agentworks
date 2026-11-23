package sessiontransfer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// Pi ("pi-cli") keeps one append-only JSONL per session, named
// <timestamp>_<id>.jsonl, in the session directory it was launched with
// (--session-dir; AgentWorks gives every native session its own). A session is
// a tree: after the header line every entry has an id and a parentId that names
// an earlier entry. Pi's session ids are either UUIDs it mints itself or the id
// AgentWorks passes with --session-id (mlp-pi-<hex>), so the exporter accepts
// both through piID.
//
// Pi refuses to resume a session whose header cwd is not the directory it runs
// in (it asks "fork this session into the current directory?"), so a moved
// session needs the destination's cwd in its header. The bundle therefore
// carries the header with a fixed placeholder cwd, the real source cwd travels
// in Bundle.Workspace, and import writes the destination cwd. The header is the
// only line that differs between computers, so the byte-prefix Ancestor check
// keeps working across a copy and a hand back.
var piID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var piStamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{3}Z$`)
var piTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z$`)

const piWorkspacePlaceholder = "@workspace"

type piHeader struct {
	Type      string  `json:"type"`
	Version   int     `json:"version"`
	ID        string  `json:"id"`
	Timestamp string  `json:"timestamp"`
	Cwd       *string `json:"cwd"`
}

func parsePiHeader(line []byte) (piHeader, error) {
	var h piHeader
	if json.Unmarshal(line, &h) != nil || h.Type != "session" || h.Version != 3 || !piID.MatchString(h.ID) || !piTimestamp.MatchString(h.Timestamp) || h.Cwd == nil {
		return piHeader{}, fmt.Errorf("invalid or unsupported Pi session header (only version 3 is supported)")
	}
	return h, nil
}

func readPiHeader(path string) (piHeader, error) {
	f, e := os.Open(path)
	if e != nil {
		return piHeader{}, e
	}
	defer f.Close()
	line, e := bufio.NewReader(io.LimitReader(f, 2<<20)).ReadBytes('\n')
	if e != nil {
		return piHeader{}, fmt.Errorf("invalid Pi session header")
	}
	return parsePiHeader(line)
}

func piFileName(h piHeader) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(h.Timestamp) + "_" + h.ID + ".jsonl"
}

// piFind returns the one session file of this id below the store, either
// directly in it (--session-dir) or one folder down (Pi's default per-cwd
// layout), as a path relative to the store. It returns os.ErrNotExist when
// there is none.
func piFind(store, id string) (string, error) {
	if !piID.MatchString(id) {
		return "", fmt.Errorf("invalid Pi session identity")
	}
	var found []string
	for _, pattern := range []string{"*_" + id + ".jsonl", "*/*_" + id + ".jsonl"} {
		matches, e := filepath.Glob(filepath.Join(store, pattern))
		if e != nil {
			return "", e
		}
		for _, m := range matches {
			// "*_id" also matches longer ids that end in "_id": the rest must be exactly a Pi stamp.
			if piStamp.MatchString(strings.TrimSuffix(filepath.Base(m), "_"+id+".jsonl")) {
				found = append(found, m)
			}
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("the native Pi session is ambiguous")
	}
	if len(found) == 0 {
		return "", os.ErrNotExist
	}
	rel, e := filepath.Rel(store, found[0])
	if e != nil {
		return "", e
	}
	return filepath.ToSlash(rel), nil
}

// piRewriteHeader returns the header line with its cwd (and, for a fork, id)
// replaced. Other header fields are kept.
func piRewriteHeader(line []byte, cwd, id string) ([]byte, error) {
	var row map[string]json.RawMessage
	if json.Unmarshal(line, &row) != nil {
		return nil, fmt.Errorf("invalid Pi session header")
	}
	row["cwd"], _ = json.Marshal(cwd)
	if id != "" {
		row["id"], _ = json.Marshal(id)
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(row); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}

// piCopyWithCwd writes a Pi journal with its header's cwd replaced; every later
// line is copied byte for byte. Like Copy it stages in the destination folder
// and renames, so a reader never sees a half-written journal.
func piCopyWithCwd(root, destination, source, cwd, id string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	reader := bufio.NewReaderSize(in, 64<<10)
	first, e := reader.ReadSlice('\n')
	if e != nil {
		return fmt.Errorf("invalid Pi session header")
	}
	header, e := piRewriteHeader(first, cwd, id)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(destination), ".native-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(header)
	if e == nil {
		_, e = io.Copy(f, reader)
	}
	if closeErr := f.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = slotfs.ShareTree(root, f.Name()); e != nil {
		return e
	}
	return os.Rename(f.Name(), destination)
}

// piDestinationCwd is the directory Pi will run in, as Pi records it (the
// physical path). A folder that does not exist yet keeps its cleaned spelling.
func piDestinationCwd(workspace string) (string, error) {
	if !filepath.IsAbs(workspace) {
		return "", fmt.Errorf("Pi import needs an absolute workspace")
	}
	if cwd, e := pathidentity.Resolve(workspace); e == nil {
		return cwd, nil
	}
	return filepath.Clean(workspace), nil
}

// piJournalCheck validates a Pi journal line by line: a version 3 header for
// this session, then entries with unique ids whose parents were seen earlier.
type piJournalCheck struct {
	seen  map[string]bool
	lines int
}

func (c *piJournalCheck) line(raw []byte, id string) error {
	if c.lines == 0 {
		c.seen = map[string]bool{}
		h, e := parsePiHeader(raw)
		if e != nil {
			return e
		}
		if h.ID != id {
			return fmt.Errorf("Pi journal belongs to another session")
		}
		if *h.Cwd != piWorkspacePlaceholder {
			return fmt.Errorf("Pi journal header was not prepared for transfer")
		}
		c.lines++
		return nil
	}
	var entry struct {
		Type   string  `json:"type"`
		ID     string  `json:"id"`
		Parent *string `json:"parentId"`
	}
	if json.Unmarshal(raw, &entry) != nil || entry.Type == "" || entry.Type == "session" || entry.ID == "" || c.seen[entry.ID] {
		return fmt.Errorf("invalid Pi journal entry")
	}
	if entry.Parent != nil && !c.seen[*entry.Parent] {
		return fmt.Errorf("Pi journal entry points to a missing parent")
	}
	c.seen[entry.ID] = true
	c.lines++
	return nil
}
