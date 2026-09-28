package accesstokens

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLifecyclePersistenceAndRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth", "tokens.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	ctx := context.Background()
	record, raw, err := s.Issue(ctx, Token{Name: "Claude", UserID: "owner", Scopes: []string{"workflows:read"}, AllWorkflows: true, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, raw, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, raw, now.Add(time.Hour)); !errors.Is(err, ErrInvalid) {
		t.Fatal("expired token accepted", err)
	}
	if err = s.Revoke(ctx, record.ID, "other", now); !errors.Is(err, ErrInvalid) {
		t.Fatal("another owner could revoke", err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	records, err := s.List(ctx, "owner")
	if err != nil || len(records) != 1 || records[0].LastUsedAt == nil {
		t.Fatalf("metadata missing: %+v %v", records, err)
	}
	// Concurrent last-used writes must not undo revocation.
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() { defer group.Done(); _, _ = s.Authenticate(ctx, raw, now) }()
	}
	if err = s.Revoke(ctx, record.ID, "owner", now); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	if _, err = s.Authenticate(ctx, raw, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("revoked token accepted", err)
	}
	for _, p := range []string{path, path + "-wal"} {
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), raw) {
			t.Fatal("plaintext token persisted")
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("database permissions", info.Mode())
	}
}
func TestWriteScopesRejected(t *testing.T) {
	now := time.Now()
	for _, scopes := range [][]string{
		append([]string{}, Scopes...),
		{"workflows:read", "files:read", "files:write"},
		{"workflows:read", "plan:write"},
		{"builder:chat"},
	} {
		tkn := Token{Name: "test", UserID: "u", Scopes: scopes, AllWorkflows: true, ExpiresAt: now.Add(time.Hour)}
		if Validate(tkn, now) == nil {
			t.Fatalf("write scopes accepted: %v", scopes)
		}
	}
	tkn := Token{Name: "test", UserID: "u", Scopes: []string{"workflows:read", "files:read"}, AllWorkflows: true, ExpiresAt: now.Add(time.Hour)}
	if err := Validate(tkn, now); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCrewScopesAndBounds(t *testing.T) {
	now := time.Now()
	base := Token{Name: "t", UserID: "u", ExpiresAt: now.Add(24 * time.Hour)}
	cases := []struct {
		name string
		edit func(*Token)
		ok   bool
	}{
		{"crew-only specific", func(t *Token) { t.Scopes = []string{"crews:read", "crews:run"}; t.CrewIDs = []string{"c1"} }, true},
		{"crew-only all", func(t *Token) { t.Scopes = []string{"crews:read"}; t.AllCrews = true }, true},
		{"crew scope without bound", func(t *Token) { t.Scopes = []string{"crews:run"} }, false},
		{"crew bound without crew scope", func(t *Token) { t.Scopes = []string{"workflows:read"}; t.AllWorkflows = true; t.AllCrews = true }, false},
		{"workflow bound without workflow scope", func(t *Token) { t.Scopes = []string{"crews:read"}; t.AllCrews = true; t.AllWorkflows = true }, false},
		{"mixed", func(t *Token) {
			t.Scopes = []string{"workflows:read", "crews:read"}
			t.WorkflowIDs = []string{"w"}
			t.CrewIDs = []string{"c"}
		}, true},
		{"workflow-only unchanged", func(t *Token) { t.Scopes = []string{"workflows:read"}; t.WorkflowIDs = []string{"w"} }, true},
	}
	for _, tc := range cases {
		token := base
		tc.edit(&token)
		if err := Validate(token, now); (err == nil) != tc.ok {
			t.Fatalf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	full := Token{Scopes: append([]string(nil), workflowScopes...), AllWorkflows: true}
	if !full.FullBuilderAccess() {
		t.Fatal("full builder access must not require Crew scopes")
	}
}

func TestStoreRoundTripsCrewBounds(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	issued, raw, err := store.Issue(context.Background(), Token{Name: "crew", UserID: "u", Scopes: []string{"crews:read"}, CrewIDs: []string{"c1", "c2"}, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Authenticate(context.Background(), raw, now)
	if err != nil || got.ID != issued.ID || !got.AllowsCrew("c2") || got.AllowsCrew("c3") || got.AllCrews {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

func TestOpenMigratesPreCrewDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, err = old.Exec(`CREATE TABLE access_tokens (
 id TEXT PRIMARY KEY, hash TEXT NOT NULL UNIQUE, user_id TEXT NOT NULL,
 username TEXT NOT NULL, email TEXT NOT NULL, provider TEXT NOT NULL,
 name TEXT NOT NULL, scopes TEXT NOT NULL, workflow_ids TEXT NOT NULL, all_workflows INTEGER NOT NULL,
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_used_at INTEGER, revoked_at INTEGER)`)
	if err == nil {
		_, err = old.Exec(`INSERT INTO access_tokens VALUES ('id1', ?, 'u','','','','old','["workflows:read"]','[]',1,?,?,NULL,NULL)`, hash(Prefix+strings.Repeat("a", 64)), now.Unix(), now.Add(time.Hour).Unix())
	}
	old.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // reopening must not fail on the already-added columns
		store, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		got, err := store.Authenticate(context.Background(), Prefix+strings.Repeat("a", 64), now)
		store.Close()
		if err != nil || got.ID != "id1" || got.AllCrews || len(got.CrewIDs) != 0 || !got.AllWorkflows {
			t.Fatalf("migrated token = %+v, %v", got, err)
		}
	}
}

func TestBuilderAccessRequiresExplicitBoundedConsent(t *testing.T) {
	now := time.Now()
	base := Token{Name: "Builder", UserID: "owner", Scopes: []string{"builder:chat", "workflows:read", "files:read", "runs:execute"}, WorkflowIDs: []string{"invoices"}, ExpiresAt: now.Add(time.Hour)}
	if !base.BuilderAccess() || Validate(base, now) != nil {
		t.Fatal("valid bounded Builder grant rejected")
	}
	for _, scope := range base.Scopes {
		token := base
		token.Scopes = nil
		for _, other := range base.Scopes {
			if other != scope {
				token.Scopes = append(token.Scopes, other)
			}
		}
		if token.BuilderAccess() {
			t.Errorf("missing %s admitted Builder", scope)
		}
	}
	for _, ids := range [][]string{nil, {""}, {" invoices"}, {"invoices", "invoices"}} {
		token := base
		token.WorkflowIDs = ids
		if token.BuilderAccess() || Validate(token, now) == nil {
			t.Errorf("invalid bounds accepted: %q", ids)
		}
	}
	token := base
	token.AllWorkflows = true
	token.WorkflowIDs = nil
	if token.BuilderAccess() || Validate(token, now) == nil {
		t.Fatal("all-workflow Builder admitted")
	}
	store, err := Open(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, raw, err := store.Issue(context.Background(), base, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Authenticate(context.Background(), raw, now)
	if err != nil || !got.BuilderAccess() || !got.AllowsWorkflow("invoices") || got.AllowsWorkflow("secret") {
		t.Fatalf("Builder roundtrip: %+v %v", got, err)
	}
}
