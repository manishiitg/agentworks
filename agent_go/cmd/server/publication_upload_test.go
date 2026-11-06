package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// PLAT-856-509 (one sync process, 6 Nov): `agentworks publish` sends its package as chunks. Only the person who
// started the publication, on a connection allowed to publish, may plan, send or publish it; the owner's package
// arrives whole through plan -> chunks -> publish and unpacks.
func TestPublicationUploadMovesAsChunksForItsOwnerOnly(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("MULTI_USER_MODE", "")
	root, state := t.TempDir(), t.TempDir()
	big := make([]byte, 300<<10) // several chunks
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"notes.md": []byte("# Notes\n"), "data.bin": big} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw := []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"source-session\"}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"Publish this.\"}]}}\n")
	archive, _, err := publication.Build(context.Background(), root, state, publication.Source{ProjectID: "published-project", History: publication.History{Provider: "codex", SessionID: "source-session", SourceCWD: root}, Raw: raw}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	pkgBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	frames := map[string][]byte{}
	pkg, err := chunks.Split(bytes.NewReader(pkgBytes), int64(len(pkgBytes)), false, func(id string, data []byte) error {
		frames[id] = append([]byte(nil), data...)
		return nil
	})
	if err != nil || len(pkg.Chunks) < 2 {
		t.Fatalf("package chunks: %d %v", len(pkg.Chunks), err)
	}
	op := uuid.NewString()
	now := time.Now().UTC()
	if err = savePublicationUpload(&publicationUpload{ID: op, ActorID: "owner", SubmissionID: "submission", ProjectID: "published-project", Name: "Published", ProfileID: crewProfileID, State: "uploading", StartedAt: now, UpdatedAt: now, Record: &publishedProject{ProfileID: crewProfileID}}); err != nil {
		t.Fatal(err)
	}
	// The preparation itself (a real Crew) is not this test's: it stops at the package arriving whole.
	activePublications.Store(op, true)
	defer activePublications.Delete(op)
	api := &StreamingAPI{}
	call := func(claims *UserClaims, method, step string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/", bytes.NewReader(body)).WithContext(context.WithValue(context.Background(), UserContextKey, claims))
		req = mux.SetURLVars(req, map[string]string{"operation": op, "step": step})
		w := httptest.NewRecorder()
		api.handlePublicationUpload(w, req)
		return w
	}
	named, _ := json.Marshal(map[string]any{"package": pkg})
	var all bytes.Buffer
	for _, c := range pkg.Chunks {
		if err = chunks.WriteFrame(&all, c.ID, frames[c.ID]); err != nil {
			t.Fatal(err)
		}
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	other := &UserClaims{UserID: "other", Username: "other"}
	readOnly := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{UserID: "owner", Scopes: []string{"crews:read"}, AllCrews: true}}
	for _, refused := range []struct {
		who    string
		claims *UserClaims
	}{{"another person", other}, {"a token that cannot publish", readOnly}} {
		for _, step := range []struct{ method, step string }{{http.MethodPost, "plan"}, {http.MethodPut, "chunks"}, {http.MethodPost, ""}} {
			body := named
			if step.step == "chunks" {
				body = all.Bytes()
			}
			if w := call(refused.claims, step.method, step.step, body); w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
				t.Fatalf("%s %s: %d %s", refused.who, step.step, w.Code, w.Body.String())
			}
		}
	}
	missing := func() []string {
		t.Helper()
		w := call(owner, http.MethodPost, "plan", named)
		var plan struct {
			Missing []string `json:"missing"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil {
			t.Fatalf("owner plan: %d %s", w.Code, w.Body.String())
		}
		return plan.Missing
	}
	if got := missing(); len(got) != len(frames) {
		t.Fatalf("refused callers stored chunks: %d missing of %d", len(got), len(frames))
	}
	if w := call(owner, http.MethodPut, "chunks", all.Bytes()); w.Code != 200 {
		t.Fatalf("owner chunks: %d %s", w.Code, w.Body.String())
	}
	if got := missing(); len(got) != 0 {
		t.Fatalf("still missing after send: %v", got)
	}
	if w := call(owner, http.MethodPost, "", named); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"state":"preparing"`)) {
		t.Fatalf("owner publish: %d %s", w.Code, w.Body.String())
	}
	dir, _ := publicationOperationDir(op)
	got, err := os.ReadFile(filepath.Join(dir, "upload.tar"))
	if err != nil || !bytes.Equal(got, pkgBytes) {
		t.Fatalf("package did not arrive whole: %v", err)
	}
	manifest, err := publication.Unpack(context.Background(), bytes.NewReader(got), t.TempDir(), t.TempDir())
	if err != nil || manifest.ProjectID != "published-project" {
		t.Fatalf("unpack: %+v %v", manifest.ProjectID, err)
	}
}
