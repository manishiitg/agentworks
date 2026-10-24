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

func TestSharedServicesAreAvailableToEveryAccount(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,work,bullpen,knowledgebase,mcp-gateway")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL", "")
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", "bullpen,knowledgebase,mcp-gateway")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"member","username":"member","role":"viewer","products":["agentworks"]},
		{"id":"empty","username":"empty","role":"viewer","products":[]},
		{"id":"disabled","username":"disabled","role":"viewer","disabled":true,"products":[]}
	]}`)
	for _, id := range []string{"member", "empty"} {
		claims := &UserClaims{UserID: id, Username: id}
		projected, _ := productAccessResponseFields(claims)["allowed_products"].([]string)
		login := userInfoWithWorkflowPermissions(UserInfo{ID: id, Username: id})
		for _, product := range []string{"bullpen", "knowledgebase", "mcp-gateway"} {
			if !userAllowedProduct(claims, product) || !containsFold(projected, product) {
				t.Fatalf("%s must open %s and see it in auth/me: %v", id, product, projected)
			}
			if !containsFold(login.AllowedProducts, product) || !userAccessAllowsProduct(userAccessForClaims(claims), product) {
				t.Fatalf("login and schedule access must include %s for %s", product, id)
			}
			if containsFold(assignableUserProducts(), product) {
				t.Fatalf("shared service %s must not be individually assignable", product)
			}
			if userAllowedProduct(&UserClaims{UserID: "disabled", Username: "disabled"}, product) {
				t.Fatalf("disabled account must not use %s", product)
			}
			if userAllowedProduct(&UserClaims{Provider: "bot_route", BotRouteProfileID: "work"}, product) {
				t.Fatalf("route identity must not gain shared service %s", product)
			}
		}
		if userAllowedProduct(claims, "work") || userAllowedProduct(claims, "video-studio") {
			t.Fatal("shared-service access must not widen other products")
		}
	}
	// Installation selection stays the upper bound, including messaging-only servers.
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "bullpen")
	claims := &UserClaims{UserID: "empty", Username: "empty"}
	for _, product := range []string{"knowledgebase", "mcp-gateway"} {
		if userAllowedProduct(claims, product) {
			t.Fatalf("uninstalled service %s must not open", product)
		}
		if projected, _ := productAccessResponseFields(claims)["allowed_products"].([]string); containsFold(projected, product) {
			t.Fatalf("uninstalled service %s must not be advertised: %v", product, projected)
		}
	}
	if shared := sharedUserProducts(); len(shared) != 1 || shared[0] != "bullpen" {
		t.Fatalf("messaging-only shared services = %v", shared)
	}
}
