package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type directoryRegistryReadsKey struct{}
type directoryRegistryRead struct {
	raw      string
	document productConversationRegistryDocument
}
type directoryRegistryReads struct {
	mu            sync.Mutex
	documents     map[string]directoryRegistryRead
	hits, decodes int
}

// sharedDirectoryRegistryReads holds one decoded registry per path for read-only readers outside a list request.
// Same contract as a scope: validated against the current bytes on every use, never handed to mutation loaders.
var sharedDirectoryRegistryReads = directoryRegistryReads{documents: map[string]directoryRegistryRead{}}

// A scope lives for one list operation; reads outside a scope share one process-wide
// cache (sharedDirectoryRegistryReads). Both cache decoded data, not read admission:
// every use still calls store.read and reuses a decode only for identical bytes.
func startDirectoryRegistryReads(ctx context.Context) (context.Context, func()) {
	if _, ok := ctx.Value(directoryRegistryReadsKey{}).(*directoryRegistryReads); ok {
		return ctx, func() {}
	}
	scope := &directoryRegistryReads{documents: map[string]directoryRegistryRead{}}
	start := time.Now()
	return context.WithValue(ctx, directoryRegistryReadsKey{}, scope), func() {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		log.Printf("[AGENTBRIDGE] event=directory_registry_reads elapsed_ms=%d decodes=%d decode_hits=%d", time.Since(start).Milliseconds(), scope.decodes, scope.hits)
	}
}

// Read-only registry readers may borrow this immutable document. Mutation
// loaders deliberately do not use it. Selected records/identities are copied
// before being returned, so callers cannot modify the scoped snapshot.
func (store productConversationRegistryStore) directoryReadDocument(ctx context.Context, path string) (productConversationRegistryDocument, error) {
	scope, ok := ctx.Value(directoryRegistryReadsKey{}).(*directoryRegistryReads)
	if !ok {
		// Outside a list request, reuse the last decode while the file bytes are unchanged. Every call still reads
		// the current file, so a change shows at once; only the JSON decode is skipped. The Mac's busy-chat poll
		// asked every few seconds for every chat and decoded the whole registry each time (heavy GC, slow laptop).
		scope = &sharedDirectoryRegistryReads
	}
	raw, exists, err := store.read(ctx, path)
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if err != nil {
		delete(scope.documents, path)
		return productConversationRegistryDocument{}, fmt.Errorf("read product conversation registry: %w", err)
	}
	if !exists {
		raw = ""
	}
	if prior, found := scope.documents[path]; found && prior.raw == raw {
		scope.hits++
		return prior.document, nil
	}
	document, err := decodeProductConversationRegistry(raw)
	if err != nil {
		delete(scope.documents, path)
		return document, err
	}
	scope.decodes++
	scope.documents[path] = directoryRegistryRead{raw: raw, document: document}
	return document, nil
}

func copyDirectoryConversation(record ProductConversationRecord) ProductConversationRecord {
	record.EnabledServers = append([]string(nil), record.EnabledServers...)
	record.SelectedSkills = append([]string(nil), record.SelectedSkills...)
	record.WorkflowContextPaths = append([]string(nil), record.WorkflowContextPaths...)
	if record.CurrentWorkflowRun != nil {
		run := *record.CurrentWorkflowRun
		record.CurrentWorkflowRun = &run
	}
	return record
}
