package agentusage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// Ledger retains only reported numbers, hashes of native deduplication IDs and
// a byte cursor. It is local-only and is erased when reporting turns off.
type Ledger struct {
	Skipped    int64               `json:"skipped"`
	Discarding bool                `json:"discarding,omitempty"`
	Offset     int64               `json:"offset"`
	Calls      map[string]Counters `json:"calls,omitempty"`
	Users      map[string]struct{} `json:"users,omitempty"`
	Seen       map[string]struct{} `json:"seen,omitempty"`
	Runs       map[string]struct{} `json:"runs,omitempty"`
	Total      Counters            `json:"total"`
	Model      string              `json:"model,omitempty"`
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
	br := bufio.NewReaderSize(r, 65536)
	var line []byte
	for {
		fragment, err := br.ReadSlice('\n')
		complete := len(fragment) > 0 && fragment[len(fragment)-1] == '\n'
		if l.Discarding {
			l.Offset += int64(len(fragment))
			if complete {
				l.Discarding = false
			}
		} else if len(line)+len(fragment) > MaxLineBytes {
			l.Skipped++
			l.Offset += int64(len(line) + len(fragment))
			line = nil
			l.Discarding = !complete
		} else {
			line = append(line, fragment...)
			if complete {
				if e := visit(line[:len(line)-1]); e != nil {
					return e
				}
				l.Offset += int64(len(line))
				line = nil
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
