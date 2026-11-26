package server

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// crewSyncMetadataBytes bounds one crew-sync request or reply (includes the existing bounded chat archive).
const crewSyncMetadataBytes int64 = 48 << 20

func crewTransferDigest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// crewSyncStagingRoot is this Crew's app-private staging folder, never in the Crew's working folder.
func crewSyncStagingRoot(id string) (string, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return "", err
	}
	p += ".staging"
	if err = agentworksclient.EnsurePrivateDirectory(p); err != nil {
		return "", err
	}
	return p, nil
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
