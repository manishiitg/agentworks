package server

import (
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
	if len(response.Tips) != 8 || len(response.Tips["code"]) != 10 || len(response.Tips["work"]) != 7 || len(response.Tips["agentworks"]) != 3 {
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
