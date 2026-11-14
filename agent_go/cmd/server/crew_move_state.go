package server

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

type crewMoveProgress struct {
	Native     []*sessiontransfer.Bundle   `json:"native,omitempty"`
	PairID     string                      `json:"pair_id"`
	TransferID string                      `json:"transfer_id"`
	Payloads   map[string]crewsync.Payload `json:"payloads"`
}

func saveCrewMoveProgress(id, pair, transfer string, payloads map[string]crewsync.Payload, native []*sessiontransfer.Bundle) error {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(p+".progress", crewMoveProgress{PairID: pair, TransferID: transfer, Payloads: payloads, Native: native}, 0600)
}

func crewSyncStatePaths(id string) ([]string, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if name == id+".json" || strings.HasPrefix(name, id+".json.") || name == id+"-files.json" || strings.HasPrefix(name, id+"-files.json.") || name == id+"-agents.json" || name == id+"-always-on" {
			paths = append(paths, filepath.Join(filepath.Dir(p), name))
		}
	}
	return paths, nil
}

// Called only after the existing owner/co-owner Crew-delete admission.
func removeCrewSyncState(id string) error {
	paths, err := crewSyncStatePaths(id)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err = os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}
