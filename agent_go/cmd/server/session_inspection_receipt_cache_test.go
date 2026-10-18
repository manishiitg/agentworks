package server

import (
	"fmt"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"testing"
)

func TestInspectionReceiptCacheRetainsRecentlyUsedEntries(t *testing.T) {
	key := func(id int) inspectionReceiptCacheKey {
		return inspectionReceiptCacheKey{Store: "lru-fixture", Execution: fmt.Sprint(id)}
	}
	target := &orchEvents.ExecutionInspectionTarget{Name: "Original"}
	for id := 0; id < 512; id++ {
		cacheInspectionReceipt(key(id), target)
	}
	hot, found := cachedInspectionReceipt(key(0))
	if !found {
		t.Fatal("initial receipt missing")
	}
	hot.Name = "Changed by caller"
	cacheInspectionReceipt(key(512), target)
	if _, found := cachedInspectionReceipt(key(1)); found {
		t.Fatal("cold receipt was not evicted")
	}
	if hot, found := cachedInspectionReceipt(key(0)); !found || hot.Name != "Original" {
		t.Fatal("overflow removed or mutated a hot receipt")
	}
	inspectionReceiptCache.Lock()
	size := len(inspectionReceiptCache.items)
	inspectionReceiptCache.Unlock()
	if size != 512 {
		t.Fatalf("cache exceeds bound: %d", size)
	}
}
