package dashboards

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONDashboardPublicationRetainsHTMLRevisions(t *testing.T) {
	docs := t.TempDir()
	root := "Crew/example"
	if err := os.MkdirAll(filepath.Join(docs, root), 0755); err != nil {
		t.Fatal(err)
	}
	call := func(req Request) Result {
		t.Helper()
		req.Root = root
		req.IncludeDraft = true
		out, err := Execute(context.Background(), docs, req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	legacy := `<!doctype html><html><body><button onclick="this.textContent='working'">Existing dashboard</button></body></html>`
	old := call(Request{Action: "create", ID: "overview", Title: "Overview", Files: map[string]string{"index.html": legacy}})
	call(Request{Action: "publish", ID: "overview", ExpectedRevision: old.Dashboard.Revision})
	source, err := os.ReadFile("testdata/json-dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(source)
	updated := call(Request{Action: "update", ID: "overview", ExpectedRevision: old.Dashboard.Revision, Files: map[string]string{"index.json": spec, "scripts/summary.py": `print('{"marker":"SCRIPT_OK"}')`}})
	page := updated.Snapshot.Files["index.html"]
	if fixture := os.Getenv("DASHBOARD_JSON_TEST_FIXTURE"); fixture != "" {
		if err := os.WriteFile(fixture, []byte(page), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(page, `</script><script>alert(1)`) || !strings.Contains(page, "dashboard-json.js") {
		t.Fatal("spec escaped into executable markup or renderer missing")
	}
	call(Request{Action: "publish", ID: "overview", ExpectedRevision: updated.Dashboard.Revision})
	historic := call(Request{Action: "get", ID: "overview", Revision: old.Dashboard.Revision})
	if historic.Snapshot.Files["index.html"] != legacy {
		t.Fatal("existing HTML revision changed")
	}
	call(Request{Action: "restore", ID: "overview", Revision: old.Dashboard.Revision, ExpectedRevision: updated.Dashboard.Revision})
	if call(Request{Action: "get", ID: "overview"}).Snapshot.Files["index.html"] != legacy {
		t.Fatal("HTML restore changed existing behavior")
	}
	// A JSON source edit cannot retain a stale automatic-page marker.
	var auto Snapshot
	if err := json.Unmarshal([]byte(`{"title":"Auto","files":{}}`), &auto); err != nil {
		t.Fatal(err)
	}
	auto.Files["index.json"] = spec
	body, err := JSONHTML(auto.Title, []byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	auto.Files["index.html"] = body + "<!-- agentworks-default-dashboard:" + hex.EncodeToString(sum[:8]) + " -->\n"
	if err := prepareJSONSnapshot(&auto); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(auto.Files["index.html"], "agentworks-default-dashboard:") {
		t.Fatal("unchanged automatic source lost its marker")
	}
	auto.Files["index.json"] = strings.Replace(spec, "Recorded sales", "Owner design", 1)
	if err := prepareJSONSnapshot(&auto); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auto.Files["index.html"], "agentworks-default-dashboard:") {
		t.Fatal("owner-authored JSON retained automatic controls")
	}
	// Direct publication cannot bypass current catalog validation.
	p := filepath.Join(docs, root, snapshotPath("overview", old.Dashboard.Revision))
	var snap Snapshot
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	snap.Files["index.json"] = `{"root":"bad","elements":{"bad":{"type":"RawHTML","props":{},"children":[]}}}`
	raw, _ = json.Marshal(snap)
	if err = os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(context.Background(), docs, Request{Root: root, Action: "publish", ID: "overview", IncludeDraft: true, ExpectedRevision: old.Dashboard.Revision}); err == nil {
		t.Fatal("invalid draft publication accepted")
	}
}

func TestJSONDashboardCatalogRejectsExecutableAndInvalidSpecs(t *testing.T) {
	cases := []string{
		`{"root":"x","elements":{"x":{"type":"RawHTML","props":{},"children":[]}}}`,
		`{"root":"x","elements":{"x":{"type":"Heading","props":{"text":2},"children":[]}}}`,
		`{"root":"x","elements":{"x":{"type":"Stack","props":{},"children":["x"]}}}`,
		`{"root":"x","elements":{"x":{"type":"Link","props":{"label":"go","href":"javascript:alert(1)"},"children":[]}}}`,
		`{"root":"x","elements":{"x":{"type":"Image","props":{"alt":"leak","src":"https://example.com/collect"},"children":[]}}}`,
		`{"root":"x","elements":{"x":{"type":"Button","props":{"label":"run"},"on":{"press":{"action":"fetch","params":{"url":"https://example.com"}}},"children":[]}}}`,
	}
	for _, source := range cases {
		if _, err := ValidateJSONSpec([]byte(source)); err == nil {
			t.Fatalf("invalid spec accepted: %s", source)
		}
	}
}
