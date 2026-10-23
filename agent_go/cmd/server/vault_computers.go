package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// Only computer names are remembered. Live connections remain the authority for
// online status and all file/command access; this file supplies offline rows.
var vaultComputersMu sync.Mutex

func vaultComputersPath(person string) string {
	root, err := workflowCLIStateRoot()
	if err != nil || person == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(person))
	return filepath.Join(root, "vault-computers", hex.EncodeToString(sum[:])+".json")
}
func knownVaultComputers(person string) []string {
	vaultComputersMu.Lock()
	defer vaultComputersMu.Unlock()
	data, _ := os.ReadFile(vaultComputersPath(person))
	var ids []string
	_ = json.Unmarshal(data, &ids)
	return ids
}
func rememberVaultComputer(person, id string) {
	if !localfiles.ValidID(id) {
		return
	}
	file := vaultComputersPath(person)
	if file == "" {
		return
	}
	vaultComputersMu.Lock()
	defer vaultComputersMu.Unlock()
	data, _ := os.ReadFile(file)
	var ids []string
	_ = json.Unmarshal(data, &ids)
	if slices.Contains(ids, id) {
		return
	}
	ids = append(ids, id)
	slices.Sort(ids)
	data, _ = json.Marshal(ids)
	if os.MkdirAll(filepath.Dir(file), 0700) == nil {
		_ = os.WriteFile(file, data, 0600)
	}
}
