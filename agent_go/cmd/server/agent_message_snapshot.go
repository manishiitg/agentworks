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
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

type agentSnapshotKey struct {
	url        string
	generation int64
}

type agentSnapshotEntry struct {
	key     agentSnapshotKey
	etag    string
	digest  [32]byte
	valid   bool
	store   agentMessageStore // Immutable shared state; callers must clone before editing.
	content string
}

type agentSnapshotFlight struct {
	done    chan struct{}
	entry   agentSnapshotEntry
	err     error
	waiters int
}

// Only metadata is locked. Validation, transfer and parsing run outside the lock.
var agentSnapshotCache struct {
	sync.Mutex
	entry   agentSnapshotEntry
	flights map[agentSnapshotKey]*agentSnapshotFlight
}

// The returned store shares backing arrays and pointers and must be treated as
// immutable. Mutations use independent readAgentMessageStore copies instead.
// Concurrent readers share one validation per endpoint/generation; sequential
// reads still validate, with no TTL or stale-on-error authority fallback.
func readAgentMessageSnapshot(ctx context.Context) (agentMessageStore, error) {
	agentMailboxReadCaller()
	for {
		key := agentSnapshotKey{getWorkspaceAPIURL() + "/api/documents/" + agentMessagesPath, agentMessageVersion.Load()}
		agentSnapshotCache.Lock()
		flight := agentSnapshotCache.flights[key]
		leader := flight == nil
		prior := agentSnapshotCache.entry
		if leader {
			flight = &agentSnapshotFlight{done: make(chan struct{})}
			if agentSnapshotCache.flights == nil {
				agentSnapshotCache.flights = make(map[agentSnapshotKey]*agentSnapshotFlight)
			}
			agentSnapshotCache.flights[key] = flight
		} else {
			flight.waiters++
		}
		agentSnapshotCache.Unlock()
		if leader {
			// One disconnected request must not cancel validation for other
			// readers. The detached operation remains bounded even if all leave.
			fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			go func() {
				defer cancel()
				entry, err := fetchAgentMessageSnapshot(fetchCtx, key, prior)
				agentSnapshotCache.Lock()
				if err == nil && agentMessageVersion.Load() == key.generation {
					agentSnapshotCache.entry = entry
				}
				flight.entry, flight.err = entry, err
				delete(agentSnapshotCache.flights, key)
				close(flight.done)
				agentSnapshotCache.Unlock()
			}()
		}
		select {
		case <-ctx.Done():
			return agentMessageStore{}, ctx.Err()
		case <-flight.done:
		}
		// A completed write while waiting requires a new validation.
		if agentMessageVersion.Load() != key.generation && flight.err == nil {
			continue
		}
		return flight.entry.store, flight.err
	}
}

func fetchAgentMessageSnapshot(ctx context.Context, key agentSnapshotKey, prior agentSnapshotEntry) (agentSnapshotEntry, error) {
	started := time.Now()
	var ioTime time.Duration
	var size int
	defer func() { agentMailboxTiming("snapshot", started, size, ioTime) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, key.url, nil)
	if err != nil {
		return agentSnapshotEntry{}, err
	}
	same := prior.valid && prior.key == key
	if same && prior.etag != "" {
		req.Header.Set("If-None-Match", prior.etag)
	}
	// Workspace service authorization is validated even for conditional reads.
	res, err := workspaceHTTPClient.Do(req)
	ioTime = time.Since(started)
	if err != nil {
		return agentSnapshotEntry{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		if !same || prior.etag == "" {
			return agentSnapshotEntry{}, fmt.Errorf("unexpected snapshot validation")
		}
		return prior, nil
	}
	if res.StatusCode == http.StatusNotFound {
		return agentSnapshotEntry{}, nil
	}
	if res.StatusCode != http.StatusOK {
		return agentSnapshotEntry{}, fmt.Errorf("snapshot unavailable (HTTP %d)", res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	ioTime, size = time.Since(started), len(raw)
	if err != nil {
		return agentSnapshotEntry{}, err
	}
	var envelope virtualtools.WorkspaceAPIResponse
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return agentSnapshotEntry{}, err
	}
	if !envelope.Success {
		return agentSnapshotEntry{}, fmt.Errorf("snapshot unavailable")
	}
	data, _ := envelope.Data.(map[string]interface{})
	if binary, _ := data["is_binary"].(bool); binary {
		return agentSnapshotEntry{}, fmt.Errorf("snapshot is not text")
	}
	content, exists := data["content"].(string)
	if !exists {
		if strings.Contains(envelope.Message, "File does not exist") || strings.Contains(envelope.Error, "File not found") || data["filepath"] != nil {
			return agentSnapshotEntry{}, nil
		}
		return agentSnapshotEntry{}, fmt.Errorf("invalid snapshot response")
	}
	digest := sha256.Sum256([]byte(content))
	if same && prior.digest == digest {
		prior.etag = res.Header.Get("ETag")
		return prior, nil
	}
	var store agentMessageStore
	if err = json.Unmarshal([]byte(content), &store); err != nil {
		return agentSnapshotEntry{}, err
	}
	return agentSnapshotEntry{key: key, etag: res.Header.Get("ETag"), digest: digest, valid: true, store: store, content: content}, nil
}

// Independently validate without joining a snapshot flight. Lookup callers
// only inspect this immutable entry; mutations decode their own private store.
func readIndependentAgentMessageSnapshot(ctx context.Context) (agentSnapshotEntry, error) {
	key := agentSnapshotKey{getWorkspaceAPIURL() + "/api/documents/" + agentMessagesPath, agentMessageVersion.Load()}
	agentSnapshotCache.Lock()
	prior := agentSnapshotCache.entry
	agentSnapshotCache.Unlock()
	entry, err := fetchAgentMessageSnapshot(ctx, key, prior)
	if err != nil {
		return agentSnapshotEntry{}, err
	}
	agentSnapshotCache.Lock()
	if agentMessageVersion.Load() == key.generation {
		agentSnapshotCache.entry = entry
	}
	agentSnapshotCache.Unlock()
	return entry, nil
}

func cloneNamedAgent(a namedMessageAgent) namedMessageAgent {
	a.Connections = append([]string(nil), a.Connections...)
	if a.Attachment != nil {
		copy := *a.Attachment
		a.Attachment = &copy
	}
	return a
}
