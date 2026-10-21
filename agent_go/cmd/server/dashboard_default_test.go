package server

import (
	"strings"
	"sync"
	"testing"
)

// The generated default dashboard shows only what the product declares: a
// button per published function (names escaped), an Ask button, a marker the
// server uses to refresh it, and no script of its own.
func TestDefaultDashboardHTMLShowsOnlyDeclaredThings(t *testing.T) {
	page := defaultDashboardHTML(defaultDashboardInfo{
		Kind: "crew", Title: `Support <b>desk</b>`, Description: "Answers  customer\nquestions.",
		Functions: []defaultDashboardFunction{{Name: "find_order", Description: "Finds an order"}, {Name: `x"><script>`, Description: ""}},
	})
	for _, want := range []string{`data-report-action="find_order"`, `data-report-chat-button`, "Ask about this dashboard", "Answers customer questions.", "Support &lt;b&gt;desk&lt;/b&gt;"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %q in the generated page", want)
		}
	}
	if strings.Contains(page, "data-report-chat-panel") || strings.Index(page, `data-report-chat-button`) < strings.Index(page, `data-report-action="find_order"`) {
		t.Fatal("the automatic page must show its content and actions before Ask, without a second inline chat")
	}
	if strings.Contains(page, "<script") || strings.Contains(page, `x"><`) {
		t.Fatal("a function name or title must be escaped and the page must not run a script")
	}
	if !unmodifiedDefaultDashboard(page) {
		t.Fatal("the refresh marker is missing")
	}
	relay := defaultDashboardHTML(defaultDashboardInfo{Kind: "relay", Title: "Invoices"})
	if strings.Contains(relay, "What it can do") || strings.Contains(relay, "data-report-action") {
		t.Fatal("a product with no functions shows no buttons")
	}
	if defaultDashboardHTML(defaultDashboardInfo{Kind: "relay", Title: "Invoices"}) != relay {
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
