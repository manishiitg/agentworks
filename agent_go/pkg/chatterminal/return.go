package chatterminal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// The original exported bytes are the return cursor. Native timestamps are
// neither unique nor an identity, and text matching would drop repeated turns.
func CheckPrefix(root string, size int64, digest string) error {
	if size <= 0 || len(digest) != 64 {
		return fmt.Errorf("missing original journal cursor")
	}
	f, e := os.Open(filepath.Join(root, "session.jsonl"))
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.CopyN(h, f, size); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("original journal prefix changed")
	}
	return nil
}

// Only the root journal travels out. Keep the original session's child/tool
// files unchanged when importing it back; neither their omission from the wire
// nor a reader's local tool result files may be mistaken for native divergence.
func MergeRootReturn(previous, incoming *sessiontransfer.Bundle) (*sessiontransfer.Bundle, error) {
	if previous == nil || incoming == nil || previous.Provider != "claude-code" || incoming.Provider != "claude-code" || len(incoming.Files) != 1 {
		return nil, fmt.Errorf("choose one Claude journal")
	}
	if e := previous.ValidateFiles(); e != nil {
		return nil, e
	}
	if e := incoming.ValidateFiles(); e != nil {
		return nil, e
	}
	priorRoot := *previous
	priorRoot.Files = map[string]sessiontransfer.File{"session.jsonl": previous.Files["session.jsonl"]}
	if e := sessiontransfer.Ancestor(&priorRoot, incoming); e != nil {
		return nil, e
	}
	merged := *incoming
	merged.Files = maps.Clone(incoming.Files)
	for name, metadata := range previous.Files {
		if name == "session.jsonl" {
			continue
		}
		source, e := sessiontransfer.Path(previous.Root, name, false)
		if e != nil {
			return nil, e
		}
		dest, e := sessiontransfer.Path(incoming.Root, name, true)
		if e != nil {
			return nil, e
		}
		in, e := os.Open(source)
		if e != nil {
			return nil, e
		}
		out, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			in.Close()
			return nil, e
		}
		_, e = io.Copy(out, in)
		in.Close()
		ce := out.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
		merged.Files[name] = metadata
	}
	if e := merged.ValidateFiles(); e != nil {
		return nil, e
	}
	return &merged, nil
}
