package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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

// Progress comes from the destination's admitted descriptors and durable file
// offsets. Reading it never waits for the working-folder/apply lock.
func crewMoveBytes(roots crewroot.Roots, binding *crewroot.SyncBinding) (done, total int64) {
	if binding == nil {
		return
	}
	p, err := crewSyncManifestPath(roots.ID)
	if err != nil {
		return
	}
	raw, err := os.ReadFile(p + ".progress")
	var progress crewMoveProgress
	if err != nil || json.Unmarshal(raw, &progress) != nil || progress.PairID != binding.PairID {
		return
	}
	dir, err := crewTransferDir(roots.ID, binding.PairID, progress.TransferID, "upload")
	if err != nil {
		return
	}
	for name, payload := range progress.Payloads {
		if !crewPayloadName(name) || !crewPayloadAllowed(name, payload) {
			return 0, 0
		}
		total += payload.Size
		if info, err := os.Lstat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
			done += min(info.Size(), payload.Size)
		}
	}
	for _, bundle := range progress.Native {
		if bundle.Validate() != nil {
			return 0, 0
		}
		for _, file := range bundle.Files {
			total += file.Size
		}
		// rsync writes temporary files. Count a native journal only after its
		// validated completion manifest was durably saved, never estimated bytes.
		p := filepath.Join(roots.Private, "native-sync", bundle.Provider+"-"+bundle.SessionID, "manifest.json")
		var complete sessiontransfer.Bundle
		if raw, e := os.ReadFile(p); e == nil && json.Unmarshal(raw, &complete) == nil {
			for name, file := range bundle.Files {
				if complete.Files[name] == file {
					done += file.Size
				}
			}
		}
	}
	if binding.LastTransfer == progress.TransferID {
		done = total
	}
	return
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
		if name == id+".json" || strings.HasPrefix(name, id+".json.") || name == id+"-files.json" || strings.HasPrefix(name, id+"-files.json.") {
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

type crewMoveReceipt struct {
	Roots           crewroot.Roots        `json:"roots"`
	Complete        bool                  `json:"complete"`
	ProjectID       string                `json:"project_id"`
	Binding         *crewroot.SyncBinding `json:"binding"`
	BackupPath      string                `json:"backup_path,omitempty"`
	LocalFolderKept string                `json:"local_folder_kept,omitempty"`
}

func crewMoveReceiptPath(id string) (string, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Dir(p)), "crew-moves", id, "receipt.json"), nil
}

// The move keeps all local content. Only a managed working folder is renamed;
// a selected folder (including its .agentworks files) is never modified here.
func finishCrewMove(ctx context.Context, roots crewroot.Roots, binding *crewroot.SyncBinding, now time.Time) (*crewMoveReceipt, error) {
	p, err := crewMoveReceiptPath(roots.ID)
	if err != nil {
		return nil, err
	}
	out := crewMoveReceipt{Roots: roots, ProjectID: roots.ID, Binding: binding}
	if raw, e := os.ReadFile(p); e == nil {
		if json.Unmarshal(raw, &out) != nil || out.Binding == nil || out.Binding.PairID != binding.PairID {
			return nil, fmt.Errorf("the saved move receipt is unavailable")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if out.Complete {
		return &out, nil
	}
	if out.BackupPath == "" && out.LocalFolderKept == "" {
		if roots.Local {
			out.LocalFolderKept = roots.Files
		} else {
			docs, e := filepath.EvalSymlinks(fsutil.WorkspaceDocsRoot())
			if e != nil {
				return nil, e
			}
			managed := filepath.Join(docs, "Crew")
			if filepath.Dir(roots.Files) != managed {
				return nil, fmt.Errorf("this Crew's managed folder is unavailable")
			}
			base := roots.Files + " (moved online " + now.Format("2006-01-02") + ")"
			out.BackupPath = base
			for n := 2; ; n++ {
				if _, e := os.Lstat(out.BackupPath); os.IsNotExist(e) {
					break
				} else if e != nil {
					return nil, e
				}
				out.BackupPath = fmt.Sprintf("%s %d", base, n)
			}
		}
		if err = fsutil.WriteJSONAtomic(p, out, 0600); err != nil {
			return nil, err
		}
	}
	if out.BackupPath != "" {
		if _, e := os.Lstat(roots.Files); e == nil {
			if _, e := os.Lstat(out.BackupPath); !os.IsNotExist(e) {
				return nil, fmt.Errorf("the move backup already exists; keep both folders and retry after checking them")
			}
			if err = os.Rename(roots.Files, out.BackupPath); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(e) {
			return nil, e
		} else if info, e := os.Lstat(out.BackupPath); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("the moved folder backup is unavailable")
		}
	}
	// Keep the private settings, transcript files and the former sync state as
	// plain backup files, but leave no active binding that offers this Crew.
	paths, err := crewSyncStatePaths(roots.ID)
	if err != nil {
		return nil, err
	}
	backup := filepath.Join(filepath.Dir(p), "sync-state")
	if err = os.MkdirAll(backup, 0700); err != nil {
		return nil, err
	}
	for _, source := range paths {
		if err = os.Rename(source, filepath.Join(backup, filepath.Base(source))); err != nil {
			return nil, err
		}
	}
	// Archive the owner's registry records, then remove only their active slots.
	// Transcripts, private settings and native journals are left as plain files.
	store := defaultProductConversationRegistryStore()
	registryPath := productConversationRegistryPath(roots.Owner)
	lock := productConversationRegistryMutex(registryPath)
	lock.Lock()
	defer lock.Unlock()
	doc, err := store.loadDocument(ctx, registryPath)
	if err != nil {
		return nil, err
	}
	archive := filepath.Join(filepath.Dir(p), "chats.json")
	if _, e := os.Stat(archive); os.IsNotExist(e) {
		var chats []crewMoveRegistryChat
		for key, c := range doc.Entries {
			if c.ProfileID == crewProfileID && canonicalCrewWorkspaceRoot(c.WorkspacePath) == roots.Virtual {
				chats = append(chats, crewMoveRegistryChat{c, doc.Previous[key], doc.Agents[key]})
			}
		}
		if err = fsutil.WriteJSONAtomic(archive, chats, 0600); err != nil {
			return nil, err
		}
	} else if e != nil {
		return nil, e
	}
	for key, c := range doc.Entries {
		if c.ProfileID == crewProfileID && canonicalCrewWorkspaceRoot(c.WorkspacePath) == roots.Virtual {
			delete(doc.Entries, key)
			delete(doc.Previous, key)
			delete(doc.Agents, key)
		}
	}
	if err = store.writeDocument(ctx, registryPath, doc); err != nil {
		return nil, err
	}
	folder := strings.TrimPrefix(roots.Virtual, "Crew/")
	if err = defaultProjectOwners().Remove("work", folder); err != nil {
		return nil, err
	}
	out.Complete = true
	if err = fsutil.WriteJSONAtomic(p, out, 0600); err != nil {
		return nil, err
	}
	return &out, nil
}

type crewMoveRegistryChat struct {
	Current  ProductConversationRecord   `json:"current"`
	Previous []ProductConversationRecord `json:"previous"`
	Identity ProductAgentIdentity        `json:"identity"`
}

func readCrewMoveReceipt(id string) (*crewMoveReceipt, error) {
	p, err := crewMoveReceiptPath(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out crewMoveReceipt
	if json.Unmarshal(raw, &out) != nil || out.ProjectID != id || out.Roots.ID != id || out.Roots.Owner == "" || out.Binding == nil {
		return nil, fmt.Errorf("the saved move receipt is unavailable")
	}
	return &out, nil
}
func crewMoveReply(receipt *crewMoveReceipt) crewSyncReply {
	return crewSyncReply{ProjectID: receipt.ProjectID, Binding: receipt.Binding, Place: "online", BackupPath: receipt.BackupPath, LocalFolderKept: receipt.LocalFolderKept}
}
