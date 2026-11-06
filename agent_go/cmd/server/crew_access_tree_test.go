package server

import "testing"

// PLAT-856-509 (owner, 6 Nov): the Share tree's Dashboards line names the actual dashboards: one shows its own name, more
// than one list each under the line, and none still offers to make one.
func TestCrewAccessDashboardLineNamesTheDashboards(t *testing.T) {
	one := crewAccessDashboardLine([]crewAccessDashboard{{Title: "Sales", Path: "db/reports/sales.html"}}, "Not shared")
	if one.Title != "Sales" || one.Path != "db/reports/sales.html" || len(one.Dashboards) != 0 {
		t.Fatalf("one dashboard: %+v", one)
	}
	two := crewAccessDashboardLine([]crewAccessDashboard{{Title: "Sales", Path: "a.html"}, {Title: "Launch plan", Path: "b.html"}}, "Not shared")
	if two.Title != "2 dashboards" || two.Path != "a.html" || len(two.Dashboards) != 2 || two.Dashboards[1].Title != "Launch plan" {
		t.Fatalf("two dashboards: %+v", two)
	}
	if none := crewAccessDashboardLine(nil, ""); none.Ask == "" || none.Title != "No dashboard" {
		t.Fatalf("no dashboard: %+v", none)
	}
}
