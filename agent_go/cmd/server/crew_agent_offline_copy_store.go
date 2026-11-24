package server

// PLAT-746-1024: source history is private runtime state, never a Team file.
// Each committed delta is immutable. Only its acknowledged cursor is public to
// the exact source uploader; this store does not grant chat or execution access.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
)

const crewOfflineDeltaLimit = chunks.BatchBytes

type crewOfflineBinding struct {
	Provider   string `json:"provider"`
	Session    string `json:"session"`
	Attachment string `json:"attachment"`
}

// A native attachment is a transport lease, not the conversation identity.
// These comparisons are private data continuity only: every HTTP producer and
// handback request still proves the exact CURRENT attachment before reading.
func sameCrewOfflineSource(a, b crewOfflineBinding) bool {
	if !a.valid() || !b.valid() || a.Provider != b.Provider || a.Session != b.Session {
		return false
	}
	if a.Provider == "claude" || a.Provider == "codex" {
		return true
	}
	return a.Attachment == b.Attachment
}

type crewOfflineCursor struct {
	Revision uint64 `json:"revision"`
	Offset   int64  `json:"offset"`
	TailHash string `json:"tail_hash"`
}

type crewOfflineDelta struct {
	crewOfflineBinding
	BaseRevision     uint64      `json:"base_revision"`
	Offset           int64       `json:"offset"`
	PreviousTailHash string      `json:"previous_tail_hash"`
	Delta            chunkedFile `json:"delta"`
	SourceSize       int64       `json:"source_size"`
}

type crewOfflineHistory struct {
	crewOfflineBinding
	crewOfflineCursor
	// Tail stays private. It never appears in the status/ack response.
	Tail       []byte        `json:"private_tail,omitempty"`
	SourceSize int64         `json:"source_size"`
	Batches    []chunkedFile `json:"batches"`
}

func crewOfflineEmptyHash() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}

// crewOfflineDirectory is where a source agent's synced chat lives. Reading never creates it (checks used to leave
// thousands of empty folders); only an accepted upload does, through crewOfflineWritableDirectory.
func crewOfflineDirectory(project, agent string) (string, error) {
	return crewOfflineDirectoryAt(project, agent, false)
}

func crewOfflineWritableDirectory(project, agent string) (string, error) {
	return crewOfflineDirectoryAt(project, agent, true)
}

func crewOfflineDirectoryAt(project, agent string, create bool) (string, error) {
	id := strings.TrimPrefix(agent, crewTeamAgentIDPrefix)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || !strings.HasPrefix(agent, crewTeamAgentIDPrefix) {
		return "", fmt.Errorf("invalid source agent")
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	// Refuse links at every private component, including existing stages.
	for _, part := range []string{"offline-copies", id} {
		dir = filepath.Join(dir, part)
		if create {
			if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
				return "", err
			}
		}
		st, e := os.Lstat(dir)
		if os.IsNotExist(e) && !create {
			continue
		}
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("private copy storage unavailable")
		}
	}
	return dir, nil
}

func (b crewOfflineBinding) valid() bool {
	if b.Provider == "agentworks" {
		return productConversationKeyPattern.MatchString(b.Session) && strings.HasPrefix(b.Attachment, "conversation-") && productConversationKeyPattern.MatchString(b.Attachment)
	}
	id, err := uuid.Parse(b.Session)
	return err == nil && id.String() == b.Session && (b.Provider == "claude" || b.Provider == "codex") && b.Attachment != ""
}

// Caller holds the source Team mutex and has freshly admitted the uploader.
// A different attachment never inherits the previous session's cursor/history.
func readCrewOfflineHistory(dir string, b crewOfflineBinding) (crewOfflineHistory, error) {
	h := crewOfflineHistory{crewOfflineBinding: b, crewOfflineCursor: crewOfflineCursor{TailHash: crewOfflineEmptyHash()}, Batches: []chunkedFile{}}
	if !b.valid() {
		return h, fmt.Errorf("invalid source session")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if os.IsNotExist(err) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	if err = json.Unmarshal(raw, &h); err != nil {
		return h, err
	}
	if !sameCrewOfflineSource(h.crewOfflineBinding, b) {
		return h, fmt.Errorf("source session changed")
	}
	h.crewOfflineBinding = b
	return h, nil
}

func validateCrewOfflineDelta(h crewOfflineHistory, in crewOfflineDelta) error {
	if !sameCrewOfflineSource(in.crewOfflineBinding, h.crewOfflineBinding) || !in.Delta.Valid() || in.Delta.Exec || in.Delta.Size <= 0 || in.Delta.Size > crewOfflineDeltaLimit || in.SourceSize < h.SourceSize || in.SourceSize < in.Offset || in.Delta.Size > in.SourceSize-in.Offset {
		return fmt.Errorf("invalid history delta")
	}
	if in.BaseRevision != h.Revision || in.Offset != h.Offset || in.PreviousTailHash != h.TailHash {
		return fmt.Errorf("history cursor changed; read its acknowledgement")
	}
	return nil
}

// This checks an exact replay before the cursor comparison. Losing a commit's
// reply must not append its tail twice; changed bytes at that offset refuse.
func crewOfflineDeltaReplayed(h crewOfflineHistory, in crewOfflineDelta) bool {
	return sameCrewOfflineSource(h.crewOfflineBinding, in.crewOfflineBinding) && h.Revision > 0 && in.BaseRevision == h.Revision-1 && in.Offset >= 0 && in.Offset+in.Delta.Size == h.Offset && len(h.Batches) > 0 && crewTeamSameVersion(h.Batches[len(h.Batches)-1], in.Delta)
}

// Source Team mutex must cover fresh admission, read, verification and save.
// No cursor is advanced until every stored chunk, complete JSONL row and hash
// matches. A failed write leaves the old cursor authoritative for retry.
func commitCrewOfflineDelta(project, agent, dir string, h crewOfflineHistory, in crewOfflineDelta) (crewOfflineCursor, error) {
	if crewOfflineDeltaReplayed(h, in) {
		return h.crewOfflineCursor, nil
	}
	if err := validateCrewOfflineDelta(h, in); err != nil {
		return h.crewOfflineCursor, err
	}
	var data bytes.Buffer
	if err := assembleChunked(filepath.Join(dir, "chunks"), in.Delta, &data); err != nil {
		return h.crewOfflineCursor, err
	}
	raw := data.Bytes()
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != in.Delta.Size || hex.EncodeToString(sum[:]) != in.Delta.Hash || raw[len(raw)-1] != '\n' {
		return h.crewOfflineCursor, fmt.Errorf("history delta is incomplete or damaged")
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 && !json.Valid(line) {
			return h.crewOfflineCursor, fmt.Errorf("history delta is not JSONL")
		}
	}
	tail := append(append([]byte(nil), h.Tail...), raw...)
	if len(tail) > 4096 {
		tail = tail[len(tail)-4096:]
	}
	sum = sha256.Sum256(tail)
	h.Tail = tail
	h.Batches = append(h.Batches, in.Delta)
	h.SourceSize = in.SourceSize
	h.Revision++
	h.Offset += in.Delta.Size
	h.TailHash = hex.EncodeToString(sum[:])
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "history.json"), h, 0600); err != nil {
		return crewOfflineCursor{}, err
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=synced revision=%d offset=%d", project, agent, h.Revision, h.Offset)
	return h.crewOfflineCursor, nil
}
