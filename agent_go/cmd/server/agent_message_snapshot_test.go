package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentSnapshotValidationAndMutationIsolation(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.Background()
	initial := agentMessageStore{Agents: []namedMessageAgent{{ID: "agent", UserID: "owner", Name: "alpha", Connections: []string{"connection"}, Attachment: &namedAgentAttachment{ID: "lease"}}}}
	if err := saveAgentMessageStore(ctx, initial); err != nil {
		t.Fatal(err)
	}
	var denied atomic.Bool
	var validated atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/documents/"+agentMessagesPath {
			if denied.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			env.mock.mu.Lock()
			raw := env.mock.files[agentMessagesPath]
			env.mock.mu.Unlock()
			tag := fmt.Sprintf("\"%x\"", sha256.Sum256([]byte(raw)))
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				validated.Add(1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		env.mock.ServeHTTP(w, r)
	}))
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	first, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readAgentMessageSnapshot(ctx)
	if err != nil || validated.Load() != 1 {
		t.Fatalf("conditional validation: %v %d", err, validated.Load())
	}
	if &first.Agents[0] != &second.Agents[0] {
		t.Fatal("unchanged snapshot decoded again")
	}
	// A caller cannot edit the cached name's connection or attachment through lookup.
	agent, err := namedAgentForClaims(ctx, &UserClaims{UserID: "owner"}, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	agent.Connections[0] = "edited"
	agent.Attachment.ID = "edited"
	if first.Agents[0].Connections[0] != "connection" || first.Agents[0].Attachment.ID != "lease" {
		t.Fatal("lookup aliases immutable cache")
	}
	mutable, err := readAgentMessageStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mutable.Agents[0].Name = "omega"
	if first.Agents[0].Name != "alpha" {
		t.Fatal("mutation reader aliases snapshot")
	}
	if err = saveAgentMessageStore(ctx, mutable); err != nil {
		t.Fatal(err)
	}
	current, err := readAgentMessageSnapshot(ctx)
	if err != nil || current.Agents[0].Name != "omega" {
		t.Fatalf("write invalidation: %v", err)
	}
	// An external same-size edit does not increment the process generation.
	mutable.Agents[0].Name = "sigma"
	raw, _ := json.Marshal(mutable)
	env.mock.mu.Lock()
	env.mock.files[agentMessagesPath] = string(raw)
	env.mock.mu.Unlock()
	current, err = readAgentMessageSnapshot(ctx)
	if err != nil || current.Agents[0].Name != "sigma" {
		t.Fatalf("external edit stale: %v", err)
	}
	denied.Store(true)
	if _, err = readAgentMessageSnapshot(ctx); err == nil {
		t.Fatal("failed workspace read reused cached authority")
	}
}

func TestAgentSnapshotConcurrentValidation(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.Background()
	if err := saveAgentMessageStore(ctx, agentMessageStore{Agents: []namedMessageAgent{{ID: "agent", UserID: "owner", Name: "alpha"}}}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var validations atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/documents/"+agentMessagesPath {
			env.mock.mu.Lock()
			raw := env.mock.files[agentMessagesPath]
			env.mock.mu.Unlock()
			tag := fmt.Sprintf("\"%x\"", sha256.Sum256([]byte(raw)))
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				if validations.Add(1) == 1 {
					close(entered)
					<-release
				}
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		env.mock.ServeHTTP(w, r)
	}))
	defer srv.Close()
	// Unblock before closing the HTTP server, including on test failure.
	defer unblock()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	if _, err := readAgentMessageSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 19)
	read := func() {
		store, err := readAgentMessageSnapshot(ctx)
		if err == nil && (len(store.Agents) != 1 || store.Agents[0].Name != "alpha") {
			err = fmt.Errorf("incorrect shared snapshot")
		}
		results <- err
	}
	leaderCtx, cancelLeader := context.WithCancel(ctx)
	defer cancelLeader()
	leaderResult := make(chan error, 1)
	go func() { _, err := readAgentMessageSnapshot(leaderCtx); leaderResult <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("validation did not start")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancelled := make(chan error, 1)
	go func() { _, err := readAgentMessageSnapshot(cancelCtx); cancelled <- err }()
	for i := 0; i < 19; i++ {
		go read()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		agentSnapshotCache.Lock()
		key := agentSnapshotKey{srv.URL + "/api/documents/" + agentMessagesPath, agentMessageVersion.Load()}
		flight := agentSnapshotCache.flights[key]
		joined := flight != nil && flight.waiters == 20
		agentSnapshotCache.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readers did not share the in-flight validation")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-cancelled:
		if err != context.Canceled {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter blocked by validation I/O")
	}
	// A mutation lookup must not wait behind the snapshot validation.
	fresh := make(chan error, 1)
	go func() {
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		_, err := namedAgentByID(ctx, "owner", "agent")
		fresh <- err
	}()
	select {
	case err := <-fresh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("mutation lookup waited behind readers")
	}
	cancelLeader()
	select {
	case err := <-leaderResult:
		if err != context.Canceled {
			t.Fatalf("leader cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("leader cancellation waited for shared I/O")
	}
	unblock()
	for i := 0; i < 19; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reader did not finish")
		}
	}
	// The mutation validates independently; the 19 snapshot waiters share
	// the first validation, even after its original caller cancels.
	if validations.Load() != 2 {
		t.Fatalf("concurrent validations: %d", validations.Load())
	}
}
