package agentusage

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// Ledger retains only reported numbers, hashes of native deduplication IDs and
// a byte cursor. It is local-only and is erased when reporting turns off.
type Ledger struct {
	Offset int64               `json:"offset"`
	Calls  map[string]Counters `json:"calls,omitempty"`
	Users  map[string]struct{} `json:"users,omitempty"`
	Seen   map[string]struct{} `json:"seen,omitempty"`
	Runs   map[string]struct{} `json:"runs,omitempty"`
	Total  Counters            `json:"total"`
	Model  string              `json:"model,omitempty"`
}

func (l *Ledger) initialize() {
	if l.Calls == nil {
		l.Calls = map[string]Counters{}
	}
	if l.Users == nil {
		l.Users = map[string]struct{}{}
	}
	if l.Seen == nil {
		l.Seen = map[string]struct{}{}
	}
	if l.Runs == nil {
		l.Runs = map[string]struct{}{}
	}
}

func numericID(id string) string { h := sha256.Sum256([]byte(id)); return hex.EncodeToString(h[:]) }

// Consume complete JSONL rows only. A bounded read ending inside a row leaves
// the cursor before it so the next hourly pass rereads that row exactly once.
func (l *Ledger) walk(r io.Reader, visit func([]byte) error) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 65536), MaxLineBytes)
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF {
			return 0, nil, nil
		}
		return 0, nil, nil
	})
	for s.Scan() {
		if err := visit(s.Bytes()); err != nil {
			return err
		}
		l.Offset += int64(len(s.Bytes()) + 1)
	}
	if s.Err() != nil {
		return ErrLimit
	}
	return nil
}
