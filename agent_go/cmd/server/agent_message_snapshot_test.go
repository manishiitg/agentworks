package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
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
	agent, err := namedAgentByID(ctx, "owner", "agent")
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
