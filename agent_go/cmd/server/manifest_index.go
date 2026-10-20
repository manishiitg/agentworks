package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The header-summary poll (every 5s per open tab) used to re-read every
// workflow.json through the workspace service whenever a 5s cache expired,
// which was nearly every poll. The index keeps parsed manifests and
// revalidates with the single folder listing discovery already makes: that
// listing carries each workflow.json's size and mtime, so only changed
// manifests are re-read. Writes made through this server mark their folder
// dirty so a same-second rewrite is never missed; a periodic full re-read
// covers anything the stamps could not see.
const manifestIndexFullReadInterval = 5 * time.Minute

var (
	manifestMutationGeneration atomic.Uint64
	manifestDirtyFolders       sync.Map // workflow folder -> struct{}
	manifestDirtyAll           atomic.Bool
)

// noteWorkspaceMutation records a workspace write or delete made by this
// server. Only workflow.json files and folder removals affect the index.
func noteWorkspaceMutation(filePath string, folder bool) {
	clean := strings.Trim(path.Clean("/"+strings.TrimSpace(filePath)), "/")
	if liveFeedReportPath(clean) {
		publishReportChanged(clean)
	}
	switch {
	case folder:
		if parts := strings.Split(clean, "/"); len(parts) >= 2 && parts[0] == "Workflow" {
			// Discovery's folder listing detects removals. Re-read only the
			// affected workflow, not every other workflow on the server.
			manifestDirtyFolders.Store(strings.Join(parts[:2], "/"), struct{}{})
		} else {
			manifestDirtyAll.Store(true)
		}
	case path.Base(clean) == "workflow.json":
		manifestDirtyFolders.Store(path.Dir(clean), struct{}{})
	case path.Base(clean) == "schedule-runs.json":
		noteScheduleSummaryChange()
		return
	default:
		return
	}
	manifestMutationGeneration.Add(1)
	noteScheduleSummaryChange()
}

// invalidateWorkflowManifestIndex forces the next discovery to re-read every
// manifest (used by explicit cache invalidation from schedule/workflow edits).
func invalidateWorkflowManifestIndex() {
	manifestDirtyAll.Store(true)
	manifestMutationGeneration.Add(1)
	noteScheduleSummaryChange()
}

type manifestStamp struct {
	modified string
	size     int64
	known    bool // the listing included this folder's children
	present  bool // workflow.json exists in the listing
}

type manifestIndexEntry struct {
	stamp    manifestStamp
	manifest *WorkflowManifest
}

type workflowManifestIndex struct {
	mu        sync.Mutex
	entries   map[string]manifestIndexEntry
	order     []string
	built     bool
	seenGen   uint64
	checkedAt time.Time
	fullAt    time.Time

	// Swappable for tests.
	list func(context.Context) ([]string, map[string]manifestStamp, error)
	read func(context.Context, string) (*WorkflowManifest, bool, error)
}

var workflowManifests = &workflowManifestIndex{
	list: listWorkflowFoldersWithManifestStamps,
	read: ReadWorkflowManifest,
}

// discover returns the current manifests, revalidating at most every
// `freshness` (or immediately after a local manifest write).
func (x *workflowManifestIndex) discover(ctx context.Context, freshness time.Duration) ([]DiscoveredWorkflow, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	gen := manifestMutationGeneration.Load()
	now := time.Now()
	if x.built && gen == x.seenGen && freshness > 0 && now.Sub(x.checkedAt) < freshness {
		return x.snapshotLocked(), nil
	}

	folders, stamps, err := x.list(ctx)
	if err != nil {
		return nil, err
	}
	full := !x.built || now.Sub(x.fullAt) >= manifestIndexFullReadInterval || manifestDirtyAll.Swap(false)

	next := make(map[string]manifestIndexEntry, len(folders))
	order := make([]string, 0, len(folders))
	for _, folder := range folders {
		stamp := stamps[folder]
		_, dirty := manifestDirtyFolders.LoadAndDelete(folder)
		if stamp.known && !stamp.present {
			continue // listed children contain no workflow.json
		}
		prev, ok := x.entries[folder]
		if ok && !full && !dirty && stamp.known && stamp == prev.stamp {
			next[folder] = prev
			order = append(order, folder)
			continue
		}
		manifest, exists, readErr := x.read(ctx, folder)
		if readErr != nil {
			log.Printf("[WARN] workflow manifest index: error reading manifest from %s: %v", folder, readErr)
			continue
		}
		if !exists {
			continue
		}
		next[folder] = manifestIndexEntry{stamp: stamp, manifest: manifest}
		order = append(order, folder)
	}

	for folder := range x.entries {
		if _, kept := next[folder]; !kept {
			manifestDirtyFolders.Delete(folder)
		}
	}
	x.entries, x.order = next, order
	x.built, x.seenGen, x.checkedAt = true, gen, now
	if full {
		x.fullAt = now
	}
	return x.snapshotLocked(), nil
}

func (x *workflowManifestIndex) snapshotLocked() []DiscoveredWorkflow {
	out := make([]DiscoveredWorkflow, 0, len(x.order))
	for _, folder := range x.order {
		out = append(out, DiscoveredWorkflow{WorkspacePath: folder, Manifest: x.entries[folder].manifest})
	}
	return out
}

// listWorkflowFoldersWithManifestStamps is listWorkspaceFolders plus each
// folder's workflow.json size/mtime from the same response (non-root
// listings include metadata).
func listWorkflowFoldersWithManifestStamps(ctx context.Context) ([]string, map[string]manifestStamp, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", getWorkspaceAPIURL()+"/api/documents", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	q := req.URL.Query()
	q.Add("folder", "Workflow")
	q.Add("max_depth", "1")
	req.URL.RawQuery = q.Encode()

	resp, err := workspaceHTTPClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return []string{}, map[string]manifestStamp{}, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("workspace API returned status %d: %s", resp.StatusCode, string(body))
	}
	return parseWorkflowFolderListing(body)
}

type workflowListingNode struct {
	Filepath     string                `json:"filepath"`
	Type         string                `json:"type"`
	Size         int64                 `json:"size"`
	LastModified string                `json:"last_modified"`
	Children     []workflowListingNode `json:"children"`
}

func parseWorkflowFolderListing(body []byte) ([]string, map[string]manifestStamp, error) {
	var apiResp struct {
		Data []workflowListingNode `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("failed to parse folder listing: %w", err)
	}
	var folders []string
	stamps := map[string]manifestStamp{}
	for _, root := range apiResp.Data {
		for _, child := range root.Children {
			if child.Type != "folder" || child.Filepath == "" {
				continue
			}
			folders = append(folders, child.Filepath)
			stamp := manifestStamp{known: child.Children != nil}
			for _, file := range child.Children {
				if path.Base(file.Filepath) == "workflow.json" && file.Type != "folder" {
					stamp.present = true
					stamp.size = file.Size
					stamp.modified = file.LastModified
					// Without an mtime the stamp cannot prove anything unchanged.
					if file.LastModified == "" {
						stamp.known = false
					}
				}
			}
			stamps[child.Filepath] = stamp
		}
	}
	return folders, stamps, nil
}
