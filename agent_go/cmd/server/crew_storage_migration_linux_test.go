//go:build linux

package server

import (
	"encoding/json"
	"errors"
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

// Real Linux archive/restore, not a mocked tar: ownership, mode, xattrs,
// symlinks and bytes must survive, while source identity and data stay put.
func TestCrewStoragePreviewAndPrivateRestore(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	withMemoryUserDirectory(t, `{"users":[{"id":"user-a","username":"Owner","admin":true,"can_create":true,"can_edit":true,"products":["agentworks","work","code"]},{"id":"user-b","username":"Member","products":["work","code"]}]}`)
	root := filepath.Join(f.Docs, filepath.FromSlash(f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)))
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
		if row.WorkspaceID == "code:"+fixtureCodeID {
			original = row
		}
	}
	if original.State != "ready" || original.Memory || original.Files == 0 || original.Name == fixtureCodeFolder {
		t.Fatalf("preview changed effective Code behavior: %+v", original)
	}
	backup := call(fixtureUserA, `{"action":"storage_backup","operation_id":"`+receipt.ID+`","workspace_id":"code:`+fixtureCodeID+`"}`)
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
	if _, err := os.Stat(filepath.Join(f.Docs, "Crew", fixtureCodeFolder)); !os.IsNotExist(err) {
		t.Fatal("preview/backup moved the project")
	}
	status := call(fixtureUserA, `{"action":"storage_status","operation_id":"`+receipt.ID+`"}`)
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), "PRIVATE_STORAGE_MARKER") || strings.Contains(status.Body.String(), `"records"`) {
		t.Fatalf("status exposed private backup contents: %d", status.Code)
	}
	// Move through the real administrator endpoint using the existing backup.
	// Rename keeps the same inode, metadata and bytes; both paths reach it.
	beforeRecord, _ := defaultProjectOwners().Lookup("code", fixtureCodeFolder)
	beforeInfo, _ := os.Stat(root)
	if denied := call(fixtureUserB, `{"action":"storage_move","operation_id":"`+receipt.ID+`"}`); denied.Code != http.StatusForbidden {
		t.Fatalf("non-admin move: %d", denied.Code)
	}
	moved := call(fixtureUserA, `{"action":"storage_move","operation_id":"`+receipt.ID+`","workspace_id":"code:`+fixtureCodeID+`"}`)
	if moved.Code != http.StatusOK {
		t.Fatalf("move: %d %s", moved.Code, moved.Body.String())
	}
	s = wait()
	for _, row := range s.Projects {
		if row.WorkspaceID == original.WorkspaceID && (row.State != "moved" || !row.RestoreVerified) {
			t.Fatalf("move receipt: %+v", row)
		}
	}
	destination := filepath.Join(f.Docs, "Crew", fixtureCodeFolder)
	destinationInfo, err := os.Stat(destination)
	if err != nil || !os.SameFile(beforeInfo, destinationInfo) {
		t.Fatal("move copied or lost the original tree", err)
	}
	after, _, _, err = crewStorageInventory(destination, io.Discard)
	if err != nil || after != original.Fingerprint {
		t.Fatal("move changed project contents or metadata", err)
	}
	link, err := filepath.EvalSymlinks(root)
	if err != nil || link != destination {
		t.Fatal("old path no longer reaches the project", err)
	}
	afterRecord, _ := defaultProjectOwners().Lookup("code", fixtureCodeFolder)
	if projectStorageRoot(afterRecord) != original.Destination || afterRecord.BindingRoot != original.Source || !registryListHas(afterRecord.Aliases, original.Source) || !registryListHas(afterRecord.Aliases, original.Destination) {
		t.Fatalf("storage bindings: %+v", afterRecord)
	}
	afterRecord.CanonicalRoot, afterRecord.BindingRoot, afterRecord.Aliases, afterRecord.Shared = beforeRecord.CanonicalRoot, beforeRecord.BindingRoot, beforeRecord.Aliases, beforeRecord.Shared
	if crewStorageHash(afterRecord) != crewStorageHash(beforeRecord) {
		t.Fatal("move changed identity, sharing or another project setting")
	}
	if err := os.WriteFile(filepath.Join(root, "after-move.txt"), []byte("through-old-link"), 0640); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "after-move.txt")); err != nil || string(data) != "through-old-link" {
		t.Fatal("old-path writes did not reach the current tree", err)
	}
	// Direct filesystem inventory retains the normal workspace boundary:
	// a settings link outside the project is unresolved, not projected.
	outside := filepath.Join(f.State, "outside-settings.json")
	raw := `{"id":"` + fixtureCodeID + `","product":"code","title":"OUTSIDE_PRIVATE_NAME"}`
	if err := os.WriteFile(outside, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "product.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "product.json")); err != nil {
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

// A request paused during snapshot loading already owns admission. A second
// request cannot replace a receipt underneath it; the next retry sees the
// durable verified receipt rather than an older pre-backup snapshot.
func TestCrewStorageBackupRetryKeepsReceipt(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	id := uuid.NewString()
	s := &crewStorageSnapshot{ID: id, State: "complete", Projects: []crewStorageProject{{Key: "code/project", RestoreVerified: true, ArchiveSHA: "verified-receipt"}}}
	if err := crewStorageSave(s); err != nil {
		t.Fatal(err)
	}
	reading, resume := make(chan struct{}), make(chan struct{})
	type result struct {
		s   *crewStorageSnapshot
		err error
	}
	done := make(chan result, 1)
	go func() {
		current, err := crewStorageAdmitBackup(id, func(id string) (*crewStorageSnapshot, error) {
			current, err := crewStorageRead(id)
			close(reading)
			<-resume
			return current, err
		})
		done <- result{current, err}
	}()
	<-reading
	_, concurrentErr := crewStorageAdmitBackup(id, crewStorageRead)
	close(resume)
	first := <-done
	defer crewStorageJobs.Delete(id)
	defer crewStorageJobs.Delete("active")
	if !errors.Is(concurrentErr, errCrewStorageBusy) || first.err != nil || first.s.Projects[0].ArchiveSHA != "verified-receipt" {
		t.Fatalf("admission lost receipt: %v %v", concurrentErr, first.err)
	}
	crewStorageJobs.Delete(id)
	crewStorageJobs.Delete("active")
	next, err := crewStorageAdmitBackup(id, crewStorageRead)
	if err != nil || !next.Projects[0].RestoreVerified || next.Projects[0].ArchiveSHA != "verified-receipt" {
		t.Fatalf("retry lost durable receipt: %v", err)
	}
}

// Moving the existing inode tree does not require recreating its file owners.
// Keep a readable, checksummed archive without claiming a scratch restore.
func TestCrewStorageMoveWithArchiveOnly(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
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
	root := filepath.Join(f.Docs, filepath.FromSlash(f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)))
	if err := os.WriteFile(filepath.Join(root, "after-preview.txt"), []byte("CURRENT_WORK"), 0640); err != nil {
		t.Fatal(err)
	}
	f.API.crewStorageMove(s, "code:"+fixtureCodeID)
	for _, row := range s.Projects {
		if row.WorkspaceID != "code:"+fixtureCodeID {
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
