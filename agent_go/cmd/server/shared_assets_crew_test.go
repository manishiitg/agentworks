package server

import (
	"encoding/json"
	"net/http/httptest"
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
