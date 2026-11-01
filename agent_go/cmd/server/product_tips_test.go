package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

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
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,work")
	tips := getProductTipsCatalog(t, nil)
	if len(tips) != 2 || len(tips["agentworks"]) == 0 || len(tips["work"]) == 0 {
		t.Fatalf("surfaces agentworks,work must serve only those catalogs, got %d surfaces", len(tips))
	}
}

func TestProductTipsFilteredByUserProducts(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"b2","username":"bob","admin":false,"can_create":true,"products":["work"]}
	]}`)
	tips := getProductTipsCatalog(t, &UserClaims{UserID: "b2", Username: "bob"})
	if len(tips["work"]) == 0 || len(tips["agentworks"]) != 0 || len(tips["example-app"]) != 0 {
		t.Fatalf("bob must see the work catalog and not the products he lacks, got %v surfaces", len(tips))
	}
}
