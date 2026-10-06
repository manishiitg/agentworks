package knowledgebase

import (
	"context"
	"encoding/base64"
	"testing"
)

// Company skills (PLAT-576) through the real tool surface: an Editor publishes a text skill but not one with scripts,
// an Owner can, get returns every file (binary assets whole), list finds it, republishing replaces the package, and
// someone without a grant sees nothing.
func TestBrainSkillsPublishGetListAndRoles(t *testing.T) {
	s, admin, priya := fixture(t, false)
	ctx := context.Background()
	folder(t, s, admin, "", "Company")
	folder(t, s, admin, "Company", "Skills")
	grant(t, s, admin, priya.IdentityID, "Company/Skills", "Editor", "grant-priya")
	tool := func(p Principal, args map[string]any) (map[string]any, error) {
		v, err := s.CallTool(ctx, p, "knowledgebase_skills", args)
		return asMap(v), err
	}
	skillMD := "---\nname: release-notes\ndescription: Write release notes from merged PRs\n---\nSteps.\n"
	logo := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	files := []any{
		map[string]any{"path": "SKILL.md", "content": skillMD},
		map[string]any{"path": "references/style.md", "content": "Be brief.\n"},
		map[string]any{"path": "assets/logo.png", "content_base64": base64.StdEncoding.EncodeToString(logo)},
	}
	if _, err := tool(priya, map[string]any{"action": "publish", "folder_path": "Company/Skills/release-notes", "files": files, "request_id": "pub-1"}); err != nil {
		t.Fatalf("an Editor publishes a skill without scripts: %v", err)
	}
	withScript := append(append([]any{}, files...), map[string]any{"path": "scripts/collect.py", "content": "print('x')\n"})
	if _, err := tool(priya, map[string]any{"action": "publish", "folder_path": "Company/Skills/release-notes", "files": withScript, "request_id": "pub-2"}); err == nil || err.(*Error).Code != "FORBIDDEN" {
		t.Fatalf("an Editor must not publish scripts: %v", err)
	}
	if _, err := tool(admin, map[string]any{"action": "publish", "folder_path": "Company/Skills/release-notes", "files": withScript, "request_id": "pub-3"}); err != nil {
		t.Fatalf("an Owner publishes scripts: %v", err)
	}
	got, err := tool(priya, map[string]any{"action": "get", "folder_path": "Company/Skills/release-notes"})
	if err != nil || got["name"] != "release-notes" || len(asSlice(got["files"])) != 4 {
		t.Fatalf("get: %v %v", got, err)
	}
	for _, f := range asSlice(got["files"]) {
		m := asMap(f)
		if m["path"] == "assets/logo.png" && m["content_base64"] != base64.StdEncoding.EncodeToString(logo) {
			t.Fatalf("binary asset must come back whole: %v", m)
		}
	}
	listed, err := tool(priya, map[string]any{"action": "list", "query": "release"})
	if err != nil || len(asSlice(listed["skills"])) != 1 || asMap(asSlice(listed["skills"])[0])["description"] != "Write release notes from merged PRs" {
		t.Fatalf("list: %v %v", listed, err)
	}
	// Republishing replaces the package.
	if _, err := tool(admin, map[string]any{"action": "publish", "folder_path": "Company/Skills/release-notes", "files": files[:1], "request_id": "pub-4"}); err != nil {
		t.Fatal(err)
	}
	if got, _ = tool(priya, map[string]any{"action": "get", "folder_path": "Company/Skills/release-notes"}); len(asSlice(got["files"])) != 1 {
		t.Fatalf("republish must remove dropped files: %v", got)
	}
	outsider := Principal{IdentityID: "editor"}
	if listed, err = tool(outsider, map[string]any{"action": "list"}); err == nil && len(asSlice(listed["skills"])) != 0 {
		t.Fatalf("no grant, no skills: %v", listed)
	}
	if _, err = tool(outsider, map[string]any{"action": "get", "folder_path": "Company/Skills/release-notes"}); err == nil {
		t.Fatal("no grant, no skill files")
	}
}
