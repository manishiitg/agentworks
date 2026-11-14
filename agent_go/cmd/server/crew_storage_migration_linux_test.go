//go:build linux

package server

import (
	"encoding/json"

	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// The one-off administrator mover still accepts a saved Work Crew at an
// old storage location. Register that explicit legacy record; the deleted
// per-user backfill no longer creates it as a fixture side effect.
func newStorageMigrationCrewFixture(t *testing.T) *multiUserFixture {
	t.Helper()
	f := newMultiUserFixture(t, legacyIdentityLayout())
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: fixtureCrewFolder,
		OwnerID: fixtureUserA, ProjectID: fixtureCrewID, Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	return f
}

// Real Linux archive/restore, not a mocked tar: ownership, mode, xattrs,
// symlinks and bytes must survive, while source identity and data stay put.
func TestCrewStoragePreviewAndPrivateRestore(t *testing.T) {
	f := newStorageMigrationCrewFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"user-a","username":"Owner","admin":true,"can_create":true,"can_edit":true,"products":["agentworks","work"]},{"id":"user-b","username":"Member","products":["work"]}]}`)
	root := filepath.Join(f.Docs, filepath.FromSlash(f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)))
	marker := filepath.Join(root, "private-marker.txt")
	if err := os.WriteFile(marker, []byte("PRIVATE_STORAGE_MARKER"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(marker, "user.storage-proof", []byte("keep"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private-marker.txt", filepath.Join(root, "marker-link")); err != nil {
		t.Fatal(err)
	}
	call := func(user, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/crew-storage", strings.NewReader(body)).WithContext(f.Ctx(user))
		f.API.handleCrewStorage(rec, req)
		return rec
	}
	if denied := call(fixtureUserB, `{"action":"storage_preview"}`); denied.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d %s", denied.Code, denied.Body.String())
	}
	created := call(fixtureUserA, `{"action":"storage_preview"}`)
	var receipt struct {
		ID string `json:"operation_id"`
	}
	if created.Code != http.StatusOK || json.Unmarshal(created.Body.Bytes(), &receipt) != nil || receipt.ID == "" {
		t.Fatalf("preview: %d %s", created.Code, created.Body.String())
	}
	wait := func() *crewStorageSnapshot {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			s, err := crewStorageRead(receipt.ID)
			_, active := crewStorageJobs.Load(receipt.ID)
			if err == nil && s.State == "complete" && !active {
				return s
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("storage operation did not finish")
		return nil
	}
	s := wait()
	var original crewStorageProject
	for _, row := range s.Projects {
		if row.WorkspaceID == "work:"+fixtureCrewID {
			original = row
		}
	}
	if original.State != "ready" || !original.Memory || original.Files == 0 || original.Name == fixtureCrewFolder {
		t.Fatalf("preview changed saved Crew behavior: %+v", original)
	}
	backup := call(fixtureUserA, `{"action":"storage_backup","operation_id":"`+receipt.ID+`","workspace_id":"work:`+fixtureCrewID+`"}`)
	if backup.Code != http.StatusOK {
		t.Fatalf("backup: %d %s", backup.Code, backup.Body.String())
	}
	s = wait()
	for _, row := range s.Projects {
		if row.WorkspaceID != original.WorkspaceID {
			continue
		}
		if row.BackupState != "project_archive_verified" || !row.RestoreVerified || row.ArchiveSHA == "" {
			t.Fatalf("restore: %+v", row)
		}
	}
	after, _, _, err := crewStorageInventory(root, io.Discard)
	if err != nil || after != original.Fingerprint {
		t.Fatalf("backup changed the source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Docs, "Crew", fixtureCrewFolder)); !os.IsNotExist(err) {
		t.Fatal("preview/backup moved the project")
	}
	status := call(fixtureUserA, `{"action":"storage_status","operation_id":"`+receipt.ID+`"}`)
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), "PRIVATE_STORAGE_MARKER") || strings.Contains(status.Body.String(), `"records"`) {
		t.Fatalf("status exposed private backup contents: %d", status.Code)
	}
	// Move through the real administrator endpoint using the existing backup.
	// Rename keeps the same inode, metadata and bytes; only the current route remains.
	beforeRecord, _ := defaultProjectOwners().Lookup("work", fixtureCrewFolder)
	beforeInfo, _ := os.Stat(root)
	if denied := call(fixtureUserB, `{"action":"storage_move","operation_id":"`+receipt.ID+`"}`); denied.Code != http.StatusForbidden {
		t.Fatalf("non-admin move: %d", denied.Code)
	}
	moved := call(fixtureUserA, `{"action":"storage_move","operation_id":"`+receipt.ID+`","workspace_id":"work:`+fixtureCrewID+`"}`)
	if moved.Code != http.StatusOK {
		t.Fatalf("move: %d %s", moved.Code, moved.Body.String())
	}
	s = wait()
	for _, row := range s.Projects {
		if row.WorkspaceID == original.WorkspaceID && (row.State != "moved" || !row.RestoreVerified) {
			t.Fatalf("move receipt: %+v", row)
		}
	}
	destination := filepath.Join(f.Docs, "Crew", fixtureCrewFolder)
	destinationInfo, err := os.Stat(destination)
	if err != nil || !os.SameFile(beforeInfo, destinationInfo) {
		t.Fatal("move copied or lost the original tree", err)
	}
	after, _, _, err = crewStorageInventory(destination, io.Discard)
	if err != nil || after != original.Fingerprint {
		t.Fatal("move changed project contents or metadata", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("retired source path remained after the move", err)
	}
	afterRecord, _ := defaultProjectOwners().Lookup("work", fixtureCrewFolder)
	if projectStorageRoot(afterRecord) != original.Destination || afterRecord.BindingRoot != original.Source || registryListHas(afterRecord.Aliases, original.Source) || registryListHas(afterRecord.Aliases, original.Destination) {
		t.Fatalf("storage bindings: %+v", afterRecord)
	}
	afterRecord.CanonicalRoot, afterRecord.BindingRoot, afterRecord.Aliases, afterRecord.Shared = beforeRecord.CanonicalRoot, beforeRecord.BindingRoot, beforeRecord.Aliases, beforeRecord.Shared
	if crewStorageHash(afterRecord) != crewStorageHash(beforeRecord) {
		t.Fatal("move changed identity, sharing or another project setting")
	}
	if err := os.WriteFile(filepath.Join(destination, "after-move.txt"), []byte("through-current-root"), 0640); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "after-move.txt")); err != nil || string(data) != "through-current-root" {
		t.Fatal("current-root writes did not reach the moved tree", err)
	}
	if _, ok := resolveCrewPath(f.Ctx(fixtureUserA), fixtureUserA, original.Source); ok {
		t.Fatal("retired source became a runtime file route")
	}
	// Direct filesystem inventory retains the normal workspace boundary:
	// a settings link outside the project is unresolved, not projected.
	outside := filepath.Join(f.State, "outside-settings.json")
	raw := `{"id":"` + fixtureCrewID + `","product":"work","title":"OUTSIDE_PRIVATE_NAME"}`
	if err := os.WriteFile(outside, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(destination, "product.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(destination, "product.json")); err != nil {
		t.Fatal(err)
	}
	created = call(fixtureUserA, `{"action":"storage_preview"}`)
	if created.Code != http.StatusOK || json.Unmarshal(created.Body.Bytes(), &receipt) != nil {
		t.Fatal("second preview failed")
	}
	s = wait()
	for _, row := range s.Projects {
		if row.WorkspaceID == original.WorkspaceID && (row.State != "needs_attention" || row.Name == "OUTSIDE_PRIVATE_NAME") {
			t.Fatal("preview followed settings outside the project")
		}
	}
}

// Moving the existing inode tree does not require recreating its file owners.
// Keep a readable, checksummed archive without claiming a scratch restore.
func TestCrewStorageMoveWithArchiveOnly(t *testing.T) {
	f := newStorageMigrationCrewFixture(t)
	s := &crewStorageSnapshot{ID: uuid.NewString(), Actor: fixtureUserA, MoveActor: fixtureUserA, State: "scanning"}
	if err := crewStorageSave(s); err != nil {
		t.Fatal(err)
	}
	f.API.crewStoragePreview(s)
	if s.State != "complete" {
		t.Fatalf("preview: %s %s", s.State, s.Error)
	}
	// A current chat can keep writing between preview and the move. This is
	// not project-busy admission: archive it now and preserve the live tree.
	root := filepath.Join(f.Docs, filepath.FromSlash(f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)))
	if err := os.WriteFile(filepath.Join(root, "after-preview.txt"), []byte("CURRENT_WORK"), 0640); err != nil {
		t.Fatal(err)
	}
	f.API.crewStorageMove(s, "work:"+fixtureCrewID)
	for _, row := range s.Projects {
		if row.WorkspaceID != "work:"+fixtureCrewID {
			continue
		}
		if row.State != "moved" || row.BackupState != "archive_verified" || row.RestoreVerified || row.ArchiveSHA == "" {
			t.Fatalf("archive-only move: %+v", row)
		}
		dir, _ := crewStorageDir(s.ID)
		sum, size, err := crewStorageFileHash(filepath.Join(dir, crewStorageHash(row.Key)+".tar.gz"))
		if err != nil || sum != row.ArchiveSHA || size != row.ArchiveBytes {
			t.Fatal("verified archive was not retained", err)
		}
		return
	}
	t.Fatal("project missing from move result")
}

// The administrator's explicit selection permits a partial backup. Bulk moves
// keep leaving incomplete inventories alone, and the rename keeps unreadable
// files and their permissions intact instead of deleting or chmodding them.
func TestCrewStorageExplicitPartialMove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires the normal non-root service permissions")
	}
	f := newStorageMigrationCrewFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"user-a","username":"Owner","admin":true,"products":["agentworks","work"]}]}`)
	root := filepath.Join(f.Docs, filepath.FromSlash(f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)))
	blocked := filepath.Join(root, "unreadable.txt")
	if err := os.WriteFile(blocked, []byte("KEEP_UNREADABLE"), 0000); err != nil {
		t.Fatal(err)
	}
	s := &crewStorageSnapshot{ID: uuid.NewString(), Actor: fixtureUserA, State: "scanning"}
	if err := crewStorageSave(s); err != nil {
		t.Fatal(err)
	}
	f.API.crewStoragePreview(s)
	var original crewStorageProject
	for _, row := range s.Projects {
		if row.WorkspaceID == "work:"+fixtureCrewID {
			original = row
		}
	}
	if original.State != "needs_attention" || !original.InventoryIncomplete {
		t.Fatalf("unreadable inventory: %+v", original)
	}
	f.API.crewStorageMove(s, "")
	if _, err := os.Lstat(root); err != nil {
		t.Fatal("bulk move touched the incomplete project", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/crew-storage", strings.NewReader(`{"action":"storage_move","operation_id":"`+s.ID+`","workspace_id":"work:`+fixtureCrewID+`"}`)).WithContext(f.Ctx(fixtureUserA))
	f.API.handleCrewStorage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit partial move: %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		current, err := crewStorageRead(s.ID)
		_, active := crewStorageJobs.Load(s.ID)
		if err == nil && current.State == "complete" && !active {
			for _, row := range current.Projects {
				if row.WorkspaceID != original.WorkspaceID {
					continue
				}
				if row.State != "moved" || row.BackupState != "archive_partial" || row.BackupSkipped != 1 || row.RestoreVerified || row.ArchiveSHA == "" {
					t.Fatalf("partial move receipt: %+v", row)
				}
				moved := filepath.Join(f.Docs, filepath.FromSlash(row.Destination), "unreadable.txt")
				info, err := os.Stat(moved)
				if err != nil || info.Mode().Perm() != 0 {
					t.Fatal("rename changed unreadable file permissions", err)
				}
				if err := os.Chmod(moved, 0600); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(moved)
				if err != nil || string(data) != "KEEP_UNREADABLE" {
					t.Fatal("rename lost unreadable bytes", err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("partial move did not complete")
}
