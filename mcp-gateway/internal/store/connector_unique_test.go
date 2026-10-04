package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAddConnectorUniqueConcurrent(t *testing.T) {
	s := NewMemoryStore()
	const attempts = 32
	var wg sync.WaitGroup
	var added atomic.Int32
	start := make(chan struct{})
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if s.AddConnectorUnique(Connector{
				ID: fmt.Sprintf("c-%d", i), WorkspaceID: "w1", Provider: "acme", InstanceSlug: "east",
			}) {
				added.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := added.Load(); got != 1 {
		t.Fatalf("successful reservations = %d, want 1", got)
	}
	if got := len(s.ListConnectors("w1")); got != 1 {
		t.Fatalf("connectors = %d, want 1", got)
	}
	if s.AddConnectorUnique(Connector{ID: "ambiguous", WorkspaceID: "w1", Provider: "acme_east"}) {
		t.Fatal("equivalent public-tool prefix was accepted")
	}
	if !s.AddConnectorUnique(Connector{ID: "other-workspace", WorkspaceID: "w2", Provider: "acme", InstanceSlug: "east"}) {
		t.Fatal("different workspace should be able to reserve the same prefix")
	}
}
