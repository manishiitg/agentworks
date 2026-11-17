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

// A scope lives only for one list operation, never on an API or global. It
// caches decoded data, not read admission: every use still calls store.read.
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
		return store.loadDocument(ctx, path)
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
