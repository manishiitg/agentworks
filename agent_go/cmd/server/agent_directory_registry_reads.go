package server

import (
	"context"
	"fmt"
	"log"
	"slices"
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
	record.agentExamples = slices.Clone(record.agentExamples)
	record.EnabledServers = slices.Clone(record.EnabledServers)
	record.SelectedSkills = slices.Clone(record.SelectedSkills)
	record.WorkflowContextPaths = slices.Clone(record.WorkflowContextPaths)
	if record.CurrentWorkflowRun != nil {
		run := *record.CurrentWorkflowRun
		record.CurrentWorkflowRun = &run
	}
	return record
}

// registryDocuments remembers each registry's last decoded document against
// the exact bytes it came from. The bytes are read on every use (the hot
// document cache makes that cheap), so a change by anyone is seen at once;
// only an unchanged document skips the decode, which on a busy laptop was
// most of the server's CPU (status checks decoded the whole registry each time).
var registryDocuments = struct {
	sync.Mutex
	entries map[string]directoryRegistryRead
	bytes   int
}{entries: map[string]directoryRegistryRead{}}

const registryDocumentsBudget = 64 << 20

// sharedDocument returns the remembered decoded registry. It is shared: only
// loadDocument calls it, and hands each caller its own copy.
func (store productConversationRegistryStore) sharedDocument(ctx context.Context, path string) (productConversationRegistryDocument, error) {
	raw, exists, err := store.read(ctx, path)
	if err != nil {
		registryDocuments.Lock()
		forgetRegistryDocumentLocked(path)
		registryDocuments.Unlock()
		return emptyProductConversationRegistry(), fmt.Errorf("read product conversation registry: %w", err)
	}
	if !exists {
		raw = ""
	}
	registryDocuments.Lock()
	if prior, found := registryDocuments.entries[path]; found && prior.raw == raw {
		registryDocuments.Unlock()
		return prior.document, nil
	}
	registryDocuments.Unlock()
	document, err := decodeProductConversationRegistry(raw)
	registryDocuments.Lock()
	defer registryDocuments.Unlock()
	forgetRegistryDocumentLocked(path)
	if err != nil {
		return document, err
	}
	if registryDocuments.bytes+len(raw) > registryDocumentsBudget {
		registryDocuments.entries, registryDocuments.bytes = map[string]directoryRegistryRead{}, 0
	}
	registryDocuments.entries[path] = directoryRegistryRead{raw: raw, document: document}
	registryDocuments.bytes += len(raw)
	return document, nil
}

func forgetRegistryDocumentLocked(path string) {
	if prior, found := registryDocuments.entries[path]; found {
		registryDocuments.bytes -= len(prior.raw)
		delete(registryDocuments.entries, path)
	}
}

// cloneProductConversationRegistry is a copy a caller may change and save.
func cloneProductConversationRegistry(document productConversationRegistryDocument) productConversationRegistryDocument {
	out := productConversationRegistryDocument{Version: document.Version, Entries: make(map[string]ProductConversationRecord, len(document.Entries))}
	for key, record := range document.Entries {
		out.Entries[key] = copyDirectoryConversation(record)
	}
	if document.Previous != nil {
		out.Previous = make(map[string][]ProductConversationRecord, len(document.Previous))
		for key, records := range document.Previous {
			copied := make([]ProductConversationRecord, len(records))
			for i, record := range records {
				copied[i] = copyDirectoryConversation(record)
			}
			out.Previous[key] = copied
		}
	}
	if document.Agents != nil {
		out.Agents = make(map[string]ProductAgentIdentity, len(document.Agents))
		for key, identity := range document.Agents {
			identity.Examples = slices.Clone(identity.Examples)
			out.Agents[key] = identity
		}
	}
	return out
}
