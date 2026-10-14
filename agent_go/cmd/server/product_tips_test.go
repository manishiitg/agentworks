package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestProductTipsExposeResolvedManifestMetadata(t *testing.T) {
	w := httptest.NewRecorder()
	(&StreamingAPI{}).handleProductTips(w, httptest.NewRequest("GET", "/api/product-tips", nil))
	if w.Code != 200 {
		t.Fatalf("tips endpoint: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Tips map[string][]agentprofiles.WorkingTip `json:"tips"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tips) != 8 || len(response.Tips["code"]) == 0 || len(response.Tips["work"]) == 0 || len(response.Tips["agentworks"]) == 0 {
		t.Fatalf("unexpected product catalog: %+v", response.Tips)
	}
	code := response.Tips["code"]
	if code[0].ID != "quick-switcher" || code[0].Text == "" || code[0].SkipIfUsed != "quick-switcher" {
		t.Fatalf("shared YAML reference was not resolved: %+v", code[0])
	}
	if code[1].When == nil || code[1].When.CodeTabs == nil || *code[1].When.CodeTabs.Max != 1 {
		t.Fatalf("Code context condition missing: %+v", code[1])
	}
	for _, tip := range response.Tips["relays"] {
		if tip.ID == "code-new-tab" {
			t.Fatal("Code-only tip leaked into Relay catalog")
		}
	}
}

func getProductTipsCatalog(t *testing.T, claims *UserClaims) map[string][]agentprofiles.WorkingTip {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/product-tips", nil)
	if claims != nil {
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, claims))
	}
	w := httptest.NewRecorder()
	(&StreamingAPI{}).handleProductTips(w, request)
	if w.Code != 200 {
		t.Fatalf("tips endpoint: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Tips map[string][]agentprofiles.WorkingTip `json:"tips"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Tips
}

func TestProductTipsFilteredByInstallationSurfaces(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "code,work")
	tips := getProductTipsCatalog(t, nil)
	if len(tips) != 2 || len(tips["code"]) == 0 || len(tips["work"]) == 0 {
		t.Fatalf("surfaces code,work must serve only those catalogs, got %d surfaces", len(tips))
	}
}

func TestProductTipsFilteredByUserProducts(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"b2","username":"bob","admin":false,"can_create":true,"products":["video-studio"]}
	]}`)
	tips := getProductTipsCatalog(t, &UserClaims{UserID: "b2", Username: "bob"})
	if len(tips["video-studio"]) == 0 || len(tips["code"]) != 0 || len(tips["work"]) != 0 {
		t.Fatalf("bob must see the video-studio catalog and not the products he lacks, got %v surfaces", len(tips))
	}
}

func TestProductTipsCatalogHasNoConflictingSharedIDs(t *testing.T) {
	tips, err := productWorkingTips()
	if err != nil {
		t.Fatalf("all-manifest tip catalog: %v", err)
	}
	if len(tips) != 8 {
		t.Fatalf("all-manifest tip catalog has %d surfaces, want 8", len(tips))
	}
}
