package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// Shared only by pure reads. Mutation transactions retain independently decoded
// stores; no caller may edit the slices or pointers in this immutable snapshot.
var agentSnapshotCache struct {
	sync.Mutex
	url        string
	etag       string
	digest     [32]byte
	generation int64
	valid      bool
	store      agentMessageStore
}

func readAgentMessageSnapshot(ctx context.Context) (agentMessageStore, error) {
	agentSnapshotCache.Lock()
	defer agentSnapshotCache.Unlock()
	cache := &agentSnapshotCache
	endpoint := getWorkspaceAPIURL() + "/api/documents/" + agentMessagesPath
	generation := agentMessageVersion.Load()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return agentMessageStore{}, err
	}
	same := cache.valid && cache.url == endpoint && cache.generation == generation
	if same && cache.etag != "" {
		req.Header.Set("If-None-Match", cache.etag)
	}
	// Always validate at the workspace service, including authorization. No TTL
	// or stale-on-error fallback; external edits must be visible to claims reads.
	res, err := workspaceHTTPClient.Do(req)
	if err != nil {
		return agentMessageStore{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		if !same {
			return agentMessageStore{}, fmt.Errorf("unexpected snapshot validation")
		}
		return cache.store, nil
	}
	if res.StatusCode == http.StatusNotFound {
		cache.valid = false
		return agentMessageStore{}, nil
	}
	if res.StatusCode != http.StatusOK {
		return agentMessageStore{}, fmt.Errorf("snapshot unavailable (HTTP %d)", res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return agentMessageStore{}, err
	}
	var envelope virtualtools.WorkspaceAPIResponse
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return agentMessageStore{}, err
	}
	if !envelope.Success {
		return agentMessageStore{}, fmt.Errorf("snapshot unavailable")
	}
	data, _ := envelope.Data.(map[string]interface{})
	if binary, _ := data["is_binary"].(bool); binary {
		return agentMessageStore{}, fmt.Errorf("snapshot is not text")
	}
	content, exists := data["content"].(string)
	if !exists {
		cache.valid = false
		if strings.Contains(envelope.Message, "File does not exist") || strings.Contains(envelope.Error, "File not found") || data["filepath"] != nil {
			return agentMessageStore{}, nil
		}
		return agentMessageStore{}, fmt.Errorf("invalid snapshot response")
	}
	digest := sha256.Sum256([]byte(content))
	if same && cache.digest == digest {
		cache.etag = res.Header.Get("ETag")
		return cache.store, nil
	}
	var store agentMessageStore
	if err = json.Unmarshal([]byte(content), &store); err != nil {
		return agentMessageStore{}, err
	}
	// A write may have completed during the fetch. Retain the starting generation
	// so a subsequent read cannot validate this old snapshot as the new version.
	cache.url, cache.etag, cache.digest, cache.generation, cache.valid, cache.store = endpoint, res.Header.Get("ETag"), digest, generation, true, store
	return store, nil
}

func cloneNamedAgent(a namedMessageAgent) namedMessageAgent {
	a.Connections = append([]string(nil), a.Connections...)
	if a.Attachment != nil {
		copy := *a.Attachment
		a.Attachment = &copy
	}
	return a
}
