package knowledgebase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Each person's "last seen" Brain commit, for the Brain tab's What's new (PLAT-633).
var seenMu sync.Mutex

func (s *Service) seenPath() string { return filepath.Join(s.private, "seen-commits.json") }

func (s *Service) SeenCommit(identity string) string {
	seenMu.Lock()
	defer seenMu.Unlock()
	all := map[string]string{}
	if b, err := os.ReadFile(s.seenPath()); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	return all[identity]
}

func (s *Service) SetSeenCommit(identity, commit string) error {
	if !commitID.MatchString(commit) {
		return badArg("commit must be a Brain commit id.")
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	all := map[string]string{}
	if b, err := os.ReadFile(s.seenPath()); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	all[identity] = commit
	return atomicJSON(s.seenPath(), all)
}
