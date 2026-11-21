package server

import (
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Crews are shared like workflows: a signed-in user with Crew access opens
// another owner's crew file/folder links read-only, never its private areas.
func TestCrewShareLinksOpenForCrewUsers(t *testing.T) {
	f := newExternalToolsFixture(t)
	const crew = "Crew/alpha"
	f.write(t, crew+"/product.json", `{"id":"crew-a"}`)
	f.write(t, crew+"/workflow.json", `{"id":"crew-a"}`)
	f.write(t, crew+"/outputs/report.md", "# Findings\n")
	f.write(t, crew+"/outputs/chart.png", "png")
	f.write(t, crew+"/builder/session-1.json", `{"private":true}`)
	f.write(t, crew+"/db/db.sqlite", "sqlite")
	allowed := map[string]bool{"owner": true, "reader": true}
	old := crewLinkReadAllowed
	crewLinkReadAllowed = func(_ *StreamingAPI, claims *UserClaims, root string) bool {
		return root == crew && allowed[claims.UserID]
	}
	t.Cleanup(func() { crewLinkReadAllowed = old })

	serve := func(user, link, operation string) *httptest.ResponseRecorder {
		t.Helper()
		var claims *UserClaims
		if user != "" {
			claims = &UserClaims{UserID: user, Username: user}
		}
		w := httptest.NewRecorder()
		f.api.servePublicAsset(w, adminRequest("GET", sharedTestURL(link)+"&uid=owner", "", claims, nil), operation)
		return w
	}

	// PLAT-746: 8106ccb9de retired per-user Crew paths; current links use the shared Crew root.
	for _, user := range []string{"owner", "reader"} {
		if w := serve(user, "Crew/alpha/outputs/report.md", "read"); w.Code != 200 || !strings.Contains(w.Body.String(), "Findings") {
			t.Fatalf("%s read file: %d %s", user, w.Code, w.Body)
		}
		if w := serve(user, "Crew/alpha/outputs", "list"); w.Code != 200 || !strings.Contains(w.Body.String(), "chart.png") {
			t.Fatalf("%s list folder: %d %s", user, w.Code, w.Body)
		}
		if w := serve(user, "Crew/alpha/outputs", "archive"); w.Code != 200 {
			t.Fatalf("%s download folder: %d %s", user, w.Code, w.Body)
		}
	}
	// PLAT-746: absolute image paths are aliases, not an access grant.
	absolute := filepath.Join(f.docs, "Crew/alpha/outputs/chart.png")
	if w := serve("reader", absolute, "read"); w.Code != 200 {
		t.Fatalf("absolute image: %d %s", w.Code, w.Body)
	}
	allowed["reader"] = false
	if w := serve("reader", absolute, "read"); w.Code != 403 {
		t.Fatalf("revoked absolute image: %d", w.Code)
	}
	allowed["reader"] = true
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := serve("reader", outside, "read"); w.Code != 403 {
		t.Fatalf("outside image: %d", w.Code)
	}
	if err := os.Symlink(outside, filepath.Join(f.docs, "Crew/alpha/outputs/escape.png")); err != nil {
		t.Fatal(err)
	}
	if w := serve("reader", filepath.Join(f.docs, "Crew/alpha/outputs/escape.png"), "read"); w.Code == 200 {
		t.Fatal("absolute image followed a symlink outside its root")
	}

	// A Crew link does not restore access to the retired owner's personal tree.
	if w := serve("reader", "_users/owner/Chats/Work/projects/alpha/outputs/report.md", "read"); w.Code != 403 {
		t.Fatalf("reader opened another person's retired Crew tree: %d %s", w.Code, w.Body)
	}

	// Readers never reach the crew's private areas, even with a direct link.
	for _, private := range []string{"builder/session-1.json", "db/db.sqlite", "product.json", "workflow.json"} {
		if w := serve("reader", "Crew/alpha/"+private, "read"); w.Code != 403 {
			t.Fatalf("reader opened private %s: %d", private, w.Code)
		}
	}
	// A reader's crew-root listing hides them; a crew-root archive is refused.
	w := serve("reader", "Crew/alpha", "list")
	if w.Code != 200 {
		t.Fatalf("reader crew root list: %d %s", w.Code, w.Body)
	}
	var listing struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listing)
	body := w.Body.String()
	if !strings.Contains(body, "outputs") || strings.Contains(body, "builder") || strings.Contains(body, "db.sqlite") || strings.Contains(body, "product.json") {
		t.Fatalf("crew root listing leaked private areas: %s", body)
	}
	if w := serve("reader", "Crew/alpha", "archive"); w.Code != 403 {
		t.Fatalf("reader archived crew root: %d", w.Code)
	}

	// No Crew access, or not signed in: rejected.
	if w := serve("outsider", "Crew/alpha/outputs/report.md", "read"); w.Code != 403 {
		t.Fatalf("user without crew access: %d", w.Code)
	}
	if w := serve("", "Crew/alpha/outputs/report.md", "read"); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	// Other personal folders of the owner stay private.
	if w := serve("reader", "Downloads/private.txt", "read"); w.Code != 403 {
		t.Fatalf("owner's Downloads opened: %d", w.Code)
	}
}

// PLAT-746: the failing screenshot lived in a chosen local Crew folder.
func TestAbsoluteCrewImageBoundFolderKeepsAdmission(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, ".agentworks"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", Owner: "owner", ID: "image-test", LocalRoot: project}}})
	state := os.Getenv("AGENTWORKS_STATE_ROOT")
	if err := os.MkdirAll(filepath.Join(state, "ownership"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(project, "shot.png")
	if err := os.WriteFile(shot, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	old := crewLinkReadAllowed
	crewLinkReadAllowed = func(_ *StreamingAPI, claims *UserClaims, root string) bool {
		return claims.UserID == "owner" && root == "Crew/example"
	}
	t.Cleanup(func() { crewLinkReadAllowed = old })
	for _, tc := range []struct {
		user, path string
		allow      bool
	}{
		{"owner", shot, true}, {"outsider", shot, false},
		{"owner", project + "/../project/shot.png", false},
		{"owner", project + "/%2e%2e/shot.png", false}, {"owner", project + "/%2fshot.png", false},
		{"owner", project + "-other/shot.png", false}, {"owner", strings.ToUpper(project) + "/shot.png", false},
		{"owner", filepath.Join(project, ".agentworks/settings/product.json"), false},
		{"owner", filepath.Join(f.docs, "_system/crew-data/image-test/private.png"), false},
		{"owner", filepath.Join(f.docs, "_users/outsider/Downloads/private.png"), false},
	} {
		w := httptest.NewRecorder()
		f.api.handlePublicFile(w, adminRequest("GET", sharedTestURL(tc.path), "", &UserClaims{UserID: tc.user, Username: tc.user}, nil))
		if (w.Code == 200) != tc.allow {
			t.Fatalf("%s %s: %d %s", tc.user, tc.path, w.Code, w.Body)
		}
	}
}
