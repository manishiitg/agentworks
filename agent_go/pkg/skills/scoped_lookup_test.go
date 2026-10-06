package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The workspace API answers a missing document with HTTP 200 AND success:true,
// putting the failure only in error/ with an empty filepath. ReadFile decoded
// neither field, returned the empty Content, and ParseSkillFile blamed the
// frontmatter — so a skill that was merely in a different folder was reported
// as malformed.
func TestReadFileTreatsTheAPIsSuccessfulNotFoundAsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "File does not exist",
			"error":   "File not found: skills/missing/SKILL.md",
			"data":    map[string]interface{}{"filepath": "", "content": ""},
		})
	}))
	defer server.Close()

	_, err := NewWorkspaceAPIClient(server.URL).ReadFile("skills/missing/SKILL.md")
	if err == nil {
		t.Fatal("a not-found answered with success:true must not read back as empty content")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should say the file is missing, got: %v", err)
	}
}

func TestParseSkillFileDoesNotBlameFrontmatterForEmptyContent(t *testing.T) {
	_, _, err := ParseSkillFile("")
	if err == nil {
		t.Fatal("empty content must be an error")
	}
	if strings.Contains(err.Error(), "frontmatter") {
		t.Fatalf("empty content must not be reported as a frontmatter defect: %v", err)
	}
}

// A resolver never reads an account-level copy after workspace discovery.
func TestGetSkillInUsesOnlyWorkspaceInventory(t *testing.T) {
	const body = "---\nname: hyperframes\ndescription: router\n---\nBody text.\n"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/skills/workspace/list" {
			t.Errorf("unexpected global read: %s", r.URL.Path)
		}
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["workspace_path"] != "Workflow/demo" {
			t.Errorf("wrong workspace: %v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"documents": []map[string]string{{"folder_name": "hyperframes", "file_path": ".agents/skills/hyperframes/SKILL.md", "document": body}}})
	}))
	defer server.Close()
	skill, err := GetSkillIn(server.URL, "Workflow/demo", "hyperframes")
	if err != nil || skill.FilePath != ".agents/skills/hyperframes/SKILL.md" {
		t.Fatalf("scoped skill = %+v %v", skill, err)
	}
	if _, err := GetSkillIn(server.URL, "", "hyperframes"); err == nil {
		t.Fatal("an unscoped read must be refused")
	}
	if requests != 1 {
		t.Fatalf("unexpected fallback or unscoped request: %d", requests)
	}
}
