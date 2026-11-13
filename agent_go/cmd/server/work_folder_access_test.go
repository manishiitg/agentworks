package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func withWorkFolderAccessFile(t *testing.T, content string) {
	t.Helper()
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	if content != "" {
		workspace.files[workFolderAccessFilePath()] = content
	}
	server := httptest.NewServer(workspace)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
}

func workFolderTestDoc(root string) string {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	return fmt.Sprintf(`{
		"assignments": {
			"alice": {"roots": [%q]},
			"bob": {"roots": []}
		},
		"grants": {
			"alice": [{"id": "g1", "alias": "site", "path": %q, "access": "read_only"}]
		}
	}`, root, root)
}

// A user sees only their own grants and roots; an unknown identity sees
// nothing. This is the read-side half of the folder authorization
// boundary: no discovery of unassigned folders through the API.
func TestWorkFolderGrantsAreScopedToCaller(t *testing.T) {
	root := t.TempDir()
	withWorkFolderAccessFile(t, workFolderTestDoc(root))
	ctx := context.Background()

	alice := &UserClaims{UserID: "u-alice", Username: "alice"}
	if grants := workFolderGrantsForClaims(ctx, alice); len(grants) != 1 || grants[0].Alias != "site" {
		t.Fatalf("alice grants = %+v", grants)
	}
	if roots := workFolderRootsForClaims(ctx, alice); len(roots) != 1 {
		t.Fatalf("alice roots = %v", roots)
	}

	bob := &UserClaims{UserID: "u-bob", Username: "BOB"}
	if grants := workFolderGrantsForClaims(ctx, bob); len(grants) != 0 {
		t.Fatalf("bob grants = %+v, want none", grants)
	}
	if roots := workFolderRootsForClaims(ctx, bob); len(roots) != 0 {
		t.Fatalf("bob roots = %v, want none", roots)
	}

	if grants := workFolderGrantsForClaims(ctx, nil); len(grants) != 0 {
		t.Fatalf("nil claims grants = %+v, want none", grants)
	}
}

// The attach decision: ordinary users stay inside their roots,
// administrators may attach anywhere, aliases stay unique per user.
func TestApplyWorkFolderAddAuthorizesAgainstRoots(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "site")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	newDoc := func() *workFolderAccessDoc {
		return &workFolderAccessDoc{
			Assignments: map[string]*workFolderAssignment{"alice": {Roots: []string{root}}},
			Grants:      map[string][]workflowtypes.WorkflowFolderGrant{},
		}
	}
	input := func(path string) workFolderAddInput {
		return workFolderAddInput{Path: path, Alias: "site", Access: "read_only"}
	}

	if _, err := applyWorkFolderAdd(newDoc(), "alice", input(inside), false); err != nil {
		t.Fatalf("in-roots attach rejected: %v", err)
	}
	if _, err := applyWorkFolderAdd(newDoc(), "alice", input(outside), false); err == nil {
		t.Fatal("out-of-roots attach accepted for ordinary user")
	}
	if _, err := applyWorkFolderAdd(newDoc(), "nobody", input(inside), false); err == nil {
		t.Fatal("attach with no assignment accepted")
	}
	if _, err := applyWorkFolderAdd(newDoc(), "alice", input(outside), true); err != nil {
		t.Fatalf("admin attach rejected: %v", err)
	}

	doc := newDoc()
	if _, err := applyWorkFolderAdd(doc, "alice", input(inside), false); err != nil {
		t.Fatalf("first attach rejected: %v", err)
	}
	if _, err := applyWorkFolderAdd(doc, "alice", input(inside), false); err == nil {
		t.Fatal("duplicate alias accepted")
	} else if _, ok := err.(errWorkFolderAliasConflict); !ok {
		t.Fatalf("expected alias-conflict error, got %T", err)
	}
}

// Deletes remove exactly one grant by id; unknown ids fail instead of
// silently succeeding.
func TestApplyWorkFolderDeleteRemovesOneGrant(t *testing.T) {
	doc := &workFolderAccessDoc{Grants: map[string][]workflowtypes.WorkflowFolderGrant{
		"alice": {
			{ID: "g1", Alias: "a"},
			{ID: "g2", Alias: "b"},
		},
	}}
	if err := applyWorkFolderDelete(doc, "alice", "g1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if len(doc.Grants["alice"]) != 1 || doc.Grants["alice"][0].ID != "g2" {
		t.Fatalf("remaining = %+v", doc.Grants["alice"])
	}
	if err := applyWorkFolderDelete(doc, "alice", "missing"); err == nil {
		t.Fatal("unknown id delete accepted")
	} else if _, ok := err.(errWorkFolderNotFound); !ok {
		t.Fatalf("expected not-found error, got %T", err)
	}
	if err := applyWorkFolderDelete(doc, "nobody", "g2"); err == nil {
		t.Fatal("delete from unknown user accepted")
	}
}

// Root replacement is strict: a missing path rejects the whole update,
// and the stored roots come back canonical.
func TestApplyWorkFolderRootsValidatesStrictly(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	insideCanonical, err := workproduct.ValidateGrant(inside, "inside", workproduct.AccessReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	outsideCanonical, err := workproduct.ValidateGrant(outside, "outside", workproduct.AccessReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	doc := &workFolderAccessDoc{
		Assignments: map[string]*workFolderAssignment{},
		Grants: map[string][]workflowtypes.WorkflowFolderGrant{
			"alice": {
				{ID: "inside", Alias: "inside", Path: insideCanonical, Access: "read_write"},
				{ID: "outside", Alias: "outside", Path: outsideCanonical, Access: "read_write"},
			},
		},
	}
	saved, err := applyWorkFolderRoots(doc, "alice", []string{root})
	if err != nil {
		t.Fatalf("valid roots rejected: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved roots = %v", saved)
	}
	if _, err := applyWorkFolderRoots(doc, "alice", []string{root, filepath.Join(root, "missing")}); err == nil {
		t.Fatal("roots with a missing path accepted")
	}
	if len(doc.Assignments["alice"].Roots) != 1 {
		t.Fatalf("failed update mutated stored roots: %+v", doc.Assignments["alice"])
	}
	if grants := doc.Grants["alice"]; len(grants) != 1 || grants[0].ID != "inside" {
		t.Fatalf("root replacement did not revoke out-of-root grants: %+v", grants)
	}
}

func TestWorkFolderAccessFallsBackAcrossIdentityKeysAndFiltersStaleGrants(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	insideCanonical, err := workproduct.ValidateGrant(inside, "inside", workproduct.AccessReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	outsideCanonical, err := workproduct.ValidateGrant(outside, "outside", workproduct.AccessReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	doc := workFolderAccessDoc{
		Assignments: map[string]*workFolderAssignment{"alice": {Roots: []string{root}}},
		Grants: map[string][]workflowtypes.WorkflowFolderGrant{"alice": {
			{ID: "inside", Alias: "inside", Path: insideCanonical, Access: "read_write"},
			{ID: "stale", Alias: "stale", Path: outsideCanonical, Access: "read_write"},
		}},
	}
	claims := &UserClaims{UserID: "u-alice", Username: "alice", Email: "alice@example.test"}
	if key := workFolderAccessKeyForClaims(doc, claims); key != "alice" {
		t.Fatalf("identity key = %q, want username-backed record", key)
	}

	oldStore := workFolderAccessStore
	workFolderAccessStore = &workFolderStore{
		load: func(context.Context) (workFolderAccessDoc, error) { return doc, nil },
		save: func(context.Context, workFolderAccessDoc) error { return nil },
	}
	t.Cleanup(func() { workFolderAccessStore = oldStore })
	grants := workFolderGrantsForClaims(context.Background(), claims)
	if len(grants) != 1 || grants[0].ID != "inside" {
		t.Fatalf("authorized grants = %+v, want only in-root grant", grants)
	}
}

// Concurrent adds from two tabs and two users must not lose grants: the
// store serializes the whole read-modify-write transaction.
func TestWorkFolderStoreSerializesConcurrentUpdates(t *testing.T) {
	withWorkFolderAccessFile(t, `{"assignments": {}, "grants": {}}`)
	ctx := context.Background()
	const perUser = 10
	var wg sync.WaitGroup
	errs := make(chan error, 2*perUser)
	for _, key := range []string{"alice", "bob"} {
		for i := 0; i < perUser; i++ {
			wg.Add(1)
			go func(user string, n int) {
				defer wg.Done()
				errs <- workFolderAccessStore.update(ctx, func(doc *workFolderAccessDoc) error {
					if doc.Grants == nil {
						doc.Grants = map[string][]workflowtypes.WorkflowFolderGrant{}
					}
					doc.Grants[user] = append(doc.Grants[user], workflowtypes.WorkflowFolderGrant{
						ID: fmt.Sprintf("%s-%d", user, n),
					})
					return nil
				})
			}(key, i)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update failed: %v", err)
		}
	}
	doc, err := workFolderAccessStore.read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Grants["alice"]) != perUser || len(doc.Grants["bob"]) != perUser {
		t.Fatalf("lost updates: alice=%d bob=%d, want %d each", len(doc.Grants["alice"]), len(doc.Grants["bob"]), perUser)
	}
}

func TestRegisterWorkFolderToolsUsesSharedGrantLifecycle(t *testing.T) {
	registrar := &recordingRegistrar{}
	if err := (&StreamingAPI{}).registerWorkFolderTools(registrar, "alice", "work-folder-test-session"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_work_folders", "attach_work_folder", "detach_work_folder"} {
		if _, ok := registrar.tools[name]; !ok {
			t.Fatalf("Work folder tool %q was not registered", name)
		}
	}
}

func TestWorkFolderToolsRefreshLiveGuardAndAliasEnvironment(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "finance")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	withWorkFolderAccessFile(t, fmt.Sprintf(`{"assignments":{"alice":{"roots":[%q]}},"grants":{"alice":[]}}`, root))

	const sessionID = "work-folder-live-refresh"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"project"}, []string{"project"})
	common.SetSessionShellEnv(sessionID, map[string]string{"DB_PATH": "/db", "WORK_FOLDER_STALE": "/stale"})

	registrar := &recordingRegistrar{}
	if err := (&StreamingAPI{}).registerWorkFolderTools(registrar, "alice", sessionID); err != nil {
		t.Fatal(err)
	}
	result, err := registrar.tools["attach_work_folder"].exec(context.Background(), map[string]interface{}{
		"path": folder, "alias": "finance", "access": "read_only",
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	var attached struct {
		Folder workflowtypes.WorkflowFolderGrant `json:"folder"`
	}
	if err := json.Unmarshal([]byte(result), &attached); err != nil {
		t.Fatalf("decode attach result: %v", err)
	}
	cfg := common.GetSessionShellConfig(sessionID)
	canonicalFolder := attached.Folder.Path
	if !containsString(cfg.ReadPaths, canonicalFolder) || !containsString(cfg.BlockedWritePaths, canonicalFolder) {
		t.Fatalf("read-only folder missing from live guard: %#v", cfg)
	}
	if cfg.Env["WORK_FOLDER_FINANCE"] != canonicalFolder {
		t.Fatalf("live alias env = %#v, want WORK_FOLDER_FINANCE=%q", cfg.Env, canonicalFolder)
	}
	if _, exists := cfg.Env["WORK_FOLDER_STALE"]; exists {
		t.Fatal("stale Work folder alias survived live refresh")
	}
	if cfg.Env["DB_PATH"] != "/db" {
		t.Fatal("live folder refresh removed an unrelated environment variable")
	}

	if _, err := registrar.tools["detach_work_folder"].exec(context.Background(), map[string]interface{}{"id": attached.Folder.ID}); err != nil {
		t.Fatalf("detach failed: %v", err)
	}
	cfg = common.GetSessionShellConfig(sessionID)
	if containsString(cfg.ReadPaths, canonicalFolder) || containsString(cfg.BlockedWritePaths, canonicalFolder) {
		t.Fatalf("detached folder remained in live guard: %#v", cfg)
	}
	if _, exists := cfg.Env["WORK_FOLDER_FINANCE"]; exists {
		t.Fatal("detached folder alias remained in live environment")
	}
}

// The background turn outlives its HTTP request. A canceled read must never
// masquerade as no attached folders (PLAT-746-959).
func TestWorkFolderTurnRejectsReadFailure(t *testing.T) {
	root := t.TempDir()
	withWorkFolderAccessFile(t, workFolderTestDoc(root))
	claims := &UserClaims{UserID: "u-alice", Username: "alice"}
	request, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := workFolderGrantsForTurn(request, claims); err == nil {
		t.Fatal("canceled authority read became an empty grant set")
	}
	turn := context.WithoutCancel(request)
	grants, err := workFolderGrantsForTurn(turn, claims)
	if err != nil || len(grants) != 1 || grants[0].Alias != "site" {
		t.Fatalf("detached turn lost stored grants: count=%d err=%v", len(grants), err)
	}
}
