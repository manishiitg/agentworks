package server

import (
	"net/http/httptest"
	"testing"
)

func withUserProductAccessFile(t *testing.T, content string) {
	t.Helper()
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	if content != "" {
		workspace.files[userProductAccessFilePath()] = content
	}
	server := httptest.NewServer(workspace)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
}

func TestUserAllowedProductDefaultsToUnrestrictedWhenFileAbsent(t *testing.T) {
	withUserProductAccessFile(t, "")

	claims := &UserClaims{UserID: "u1", Username: "reader"}
	if !userAllowedProduct(claims, "agentworks") {
		t.Fatal("user with no config entry should be unrestricted")
	}
	if !userAllowedWorkflowID(claims, "demo-workflow") {
		t.Fatal("user with no config entry should see every workflow")
	}
}

func TestAdminOnlyProductsAreHiddenAndRejectedForMembers(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", " work ")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"admin","username":"admin","admin":true,"can_create":true,"products":[]},
		{"id":"member","username":"member","can_create":true,"products":[]}
	]}`)

	admin := &UserClaims{UserID: "admin", Username: "admin"}
	member := &UserClaims{UserID: "member", Username: "member"}
	if !userAllowedProduct(admin, "work") {
		t.Fatal("admin must retain access to an admin-only product")
	}
	if userAllowedProduct(member, "work") {
		t.Fatal("member must not reach an admin-only product")
	}
	if !userAllowedProduct(member, "agentworks") {
		t.Fatal("admin-only Work must not remove the member's AgentWorks access")
	}
	products, ok := productAccessResponseFields(member)["allowed_products"].([]string)
	if !ok {
		t.Fatal("member must receive an explicit frontend product allowlist")
	}
	for _, product := range products {
		if product == "work" {
			t.Fatalf("member frontend allowlist contains admin-only Work: %v", products)
		}
	}
	if got := productAccessResponseFields(admin)["allowed_products"]; got != nil {
		t.Fatalf("admin should remain unrestricted, got %v", got)
	}
}

func TestProductsAvailableToAllAugmentReadOnlyAccounts(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL", " work ")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"reader","username":"reader","can_create":false,"products":["agentworks"]},
		{"id":"new-reader","username":"new-reader","can_create":false,"products":[]}
	]}`)

	for _, userID := range []string{"reader", "new-reader"} {
		claims := &UserClaims{UserID: userID, Username: userID}
		if !userAllowedProduct(claims, "work") {
			t.Fatalf("%s should receive deployment-wide Work access", userID)
		}
		products, ok := productAccessResponseFields(claims)["allowed_products"].([]string)
		hasWork := false
		for _, product := range products {
			hasWork = hasWork || product == "work"
		}
		if !ok || !hasWork {
			t.Fatalf("%s frontend products = %v, want Work", userID, products)
		}
	}
	if userAllowedProduct(&UserClaims{UserID: "new-reader", Username: "new-reader"}, "video-studio") {
		t.Fatal("deployment-wide Work access must not unlock unrelated products")
	}
}

func TestUserAllowedProductRestrictsExplicitEntry(t *testing.T) {
	withUserProductAccessFile(t, `{
		"reader": { "products": ["video-studio"] },
		"owner": { "products": ["video-studio", "agentworks"], "workflow_ids": ["demo-workflow"] }
	}`)

	reader := &UserClaims{UserID: "u-reader", Username: "reader"}
	if !userAllowedProduct(reader, "video-studio") {
		t.Fatal("reader should be allowed video-studio")
	}
	if userAllowedProduct(reader, "agentworks") {
		t.Fatal("reader should not be allowed agentworks")
	}
	// reader has no workflow_ids entry, so workflow access is unrestricted --
	// products and workflows are independent narrowings.
	if !userAllowedWorkflowID(reader, "demo-workflow") {
		t.Fatal("reader with no workflow_ids entry should be unrestricted for workflows")
	}

	owner := &UserClaims{UserID: "u-owner", Username: "owner"}
	if !userAllowedProduct(owner, "agentworks") {
		t.Fatal("owner should be allowed agentworks")
	}
	if !userAllowedWorkflowID(owner, "demo-workflow") {
		t.Fatal("owner should be allowed demo-workflow")
	}
	if userAllowedWorkflowID(owner, "some-other-workflow") {
		t.Fatal("owner should not be allowed a workflow outside his explicit list")
	}
}

func TestUserAllowedProductMatchesByUsernameCaseInsensitive(t *testing.T) {
	withUserProductAccessFile(t, `{ "Reader": { "products": ["video-studio"] } }`)

	claims := &UserClaims{UserID: "u-reader", Username: "reader"}
	if userAllowedProduct(claims, "agentworks") {
		t.Fatal("normalized username match should still restrict")
	}
	if !userAllowedProduct(claims, "video-studio") {
		t.Fatal("normalized username match should still allow the granted product")
	}
}

func TestFilterWorkflowManifestsForUserNarrowsList(t *testing.T) {
	withUserProductAccessFile(t, `{ "owner": { "products": ["video-studio", "agentworks"], "workflow_ids": ["demo-workflow"] } }`)

	discovered := []DiscoveredWorkflow{
		{WorkspacePath: "Workflow/demo-workflow", Manifest: &WorkflowManifest{ID: "demo-workflow"}},
		{WorkspacePath: "Workflow/other", Manifest: &WorkflowManifest{ID: "other"}},
	}

	owner := &UserClaims{UserID: "u-owner", Username: "owner"}
	filtered := filterWorkflowManifestsForUser(owner, discovered)
	if len(filtered) != 1 || filtered[0].Manifest.ID != "demo-workflow" {
		t.Fatalf("filtered = %+v, want only demo-workflow", filtered)
	}

	unrestricted := &UserClaims{UserID: "u-other", Username: "someone-else"}
	if got := filterWorkflowManifestsForUser(unrestricted, discovered); len(got) != 2 {
		t.Fatalf("unrestricted user should see every workflow, got %d", len(got))
	}
}

func TestProductAccessResponseFieldsOmitsWhenUnrestricted(t *testing.T) {
	withUserProductAccessFile(t, "")

	claims := &UserClaims{UserID: "u1", Username: "someone"}
	fields := productAccessResponseFields(claims)
	if fields["allowed_products"] != nil {
		t.Fatalf("allowed_products = %v, want nil for unrestricted user", fields["allowed_products"])
	}
	if fields["allowed_workflow_ids"] != nil {
		t.Fatalf("allowed_workflow_ids = %v, want nil for unrestricted user", fields["allowed_workflow_ids"])
	}
}

func TestBrainIsAvailableToAccountsLimitedToOtherProducts(t *testing.T) {
	withUserProductAccessFile(t, `{ "coder": { "products": ["code"] } }`)
	coder := &UserClaims{UserID: "u-coder", Username: "coder"}
	if !userAllowedProduct(coder, "knowledgebase") {
		t.Fatal("an account limited to Code should still have Brain")
	}
	if userAllowedProduct(coder, "video-studio") {
		t.Fatal("the Brain rule must not widen other products")
	}
	fields := productAccessResponseFields(coder)
	if got, _ := fields["allowed_products"].([]string); !containsFold(got, "knowledgebase") || !containsFold(got, "code") || containsFold(got, "video-studio") {
		t.Fatalf("a Code-only account's projected products should be code plus Brain, got %v", fields["allowed_products"])
	}
	// The widening alone never turns Brain on: a server that does not run it still refuses the account.
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,code")
	if knowledgebaseProductAllowed(coder) || knowledgebaseMCPAllowed(coder) {
		t.Fatal("a server without Brain must refuse every account")
	}
	if got, _ := productAccessResponseFields(coder)["allowed_products"].([]string); containsFold(got, "knowledgebase") {
		t.Fatalf("a server without Brain must not list it, got %v", got)
	}
}
