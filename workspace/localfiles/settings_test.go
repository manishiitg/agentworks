package localfiles

import (
	"os"
	"path/filepath"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func TestSettingsConnectionFolderPermissions(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "sample.bin"), []byte{0, 255, 10}, 0600); err != nil {
		t.Fatal(err)
	}
	executor, err := OpenSettings("laptop", filepath.Join(base, "state"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	if len(executor.Hello.Resources) != 0 {
		t.Fatal("connecting granted a folder")
	}
	browse := executor.Execute(t.Context(), Request{ID: "browse", Operation: "browse_folders", Path: project})
	if browse.Status != 200 || browse.Directory == nil || len(browse.Directory.Folders) != 0 {
		t.Fatalf("browse returned file contents or failed: %+v", browse)
	}
	request := Request{ID: "copy", CrewID: "crew-a", ResourceID: "project", Folders: []FolderSelection{{ID: "project", Path: project, Access: "read"}}, Operation: "copy_read", Path: "sample.bin"}
	result := executor.Execute(t.Context(), request)
	if result.Status != 200 || string(result.Bytes) != string([]byte{0, 255, 10}) {
		t.Fatalf("copy: %+v", result)
	}
	request.Operation, request.Path, request.Command, request.RequestID = "shell", ".", "touch denied", "read-command"
	if result = executor.Execute(t.Context(), request); result.Status != 403 {
		t.Fatal("Read allowed arbitrary commands")
	}
	request.Operation, request.Path, request.Content, request.ExpectedRevision = "write", "new.txt", "test", wf.MissingRevision
	if result = executor.Execute(t.Context(), request); result.Status != 403 {
		t.Fatal("Read allowed an edit")
	}
	request.Folders[0].Access = "write"
	if result = executor.Execute(t.Context(), request); result.Status != 200 || result.Receipt == nil || !result.Receipt.Applied {
		t.Fatalf("changed permission did not take effect: %+v", result)
	}
	request.Folders[0].Access = "read"
	request.RequestID = "after-downgrade"
	if result = executor.Execute(t.Context(), request); result.Status != 403 {
		t.Fatal("downgrade was cached")
	}
	request.Folders = nil
	if result = executor.Execute(t.Context(), request); result.Status != 403 {
		t.Fatal("removed grant was cached")
	}
}
