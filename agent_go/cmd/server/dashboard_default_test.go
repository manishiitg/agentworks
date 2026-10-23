package server

import (
	"strings"
	"sync"
	"testing"

	"os"
)

// The generated default dashboard shows only what the product declares: a
// button per published function (names escaped), an Ask button, a marker the
// server uses to refresh it, and shared TSX source that never invokes a function on load.
func TestDefaultDashboardHTMLShowsOnlyDeclaredThings(t *testing.T) {
	files, err := defaultDashboardFiles(defaultDashboardInfo{
		Kind: "crew", Title: `Support <b>desk</b>`, Description: "Answers  customer\nquestions.",
		Functions: []defaultDashboardFunction{{Name: "find_order", Description: "Finds an order"}, {Name: `x"><script>`, Description: ""}},
	})
	if err != nil {
		t.Fatal(err)
	}
	page := files["index.html"]
	for _, want := range []string{"FunctionButton", "AskButton", "Ask about this dashboard", "Answers customer questions.", "Support &lt;b&gt;desk&lt;/b&gt;", "dashboard-react.js", "dashboard-react.css"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %q in generated page", want)
		}
	}
	if _, ok := files["index.json"]; ok {
		t.Fatal("automatic page retained JSON entry")
	}
	if source := files["index.tsx"]; !strings.Contains(source, "@agentworks/dashboard") || !strings.Contains(source, "report.chat?.available") || strings.Contains(source, "ChatPanel") || strings.Contains(source, "report.run(") {
		t.Fatal("starting page lost shared components or shared-only Ask")
	}
	if fixture := os.Getenv("DASHBOARD_DEFAULT_TEST_FIXTURE"); fixture != "" {
		if err := os.WriteFile(fixture, []byte(page), 0600); err != nil {
			t.Fatal(err)
		}
	}

	if strings.Count(page, "</script>") != 2 {
		t.Fatal("a function name or title must be escaped and only the trusted renderer script may run")
	}
	if !unmodifiedDefaultDashboard(page) {
		t.Fatal("the refresh marker is missing")
	}
	relay, err := defaultDashboardHTML(defaultDashboardInfo{Kind: "relay", Title: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(relay, `data-report-renderer="tsx"`) {
		t.Fatal("a product with no functions still uses the shared TSX page")
	}
	again, err := defaultDashboardHTML(defaultDashboardInfo{Kind: "relay", Title: "Invoices"})
	if err != nil || again != relay {
		t.Fatal("the same declarations must give the same page, so an unchanged product is not rewritten")
	}
}

// Two first opens of the same product must not both create the default: the
// lock for one project root is exclusive, and different projects do not wait.
func TestDefaultDashboardLockIsExclusivePerProject(t *testing.T) {
	var inside, worst int
	var guard sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockDefaultDashboard("Crew/one")
			guard.Lock()
			inside++
			if inside > worst {
				worst = inside
			}
			guard.Unlock()
			guard.Lock()
			inside--
			guard.Unlock()
			unlock()
		}()
	}
	wg.Wait()
	if worst != 1 {
		t.Fatalf("two opens held the same project's lock at once (%d)", worst)
	}
	unlockOther := lockDefaultDashboard("Crew/two")
	unlockOther()
}
