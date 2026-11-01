package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// Expose only display metadata; prompts, tool policies and dependencies stay server-side.
func productWorkingTips() (map[string][]agentprofiles.WorkingTip, error) {
	loaders := map[string]func() (agentprofiles.ProductManifest, error){
		"agentworks":    agentworksproduct.AgentWorksManifest,
		"relays":        relayproduct.Manifest,
		"work":          workproduct.WorkManifest,
		"knowledgebase": knowledgebaseproduct.Manifest,
		"mcp-gateway":   func() (agentprofiles.ProductManifest, error) { return caplayerproduct.Manifest(), nil },
	}
	result := make(map[string][]agentprofiles.WorkingTip, len(loaders))
	definitions := make(map[string]agentprofiles.WorkingTip)
	for surface, load := range loaders {
		manifest, err := load()
		if err != nil {
			return nil, fmt.Errorf("%s tips: %w", surface, err)
		}
		for _, tip := range manifest.UI.Tips {
			if previous, ok := definitions[tip.ID]; ok && !reflect.DeepEqual(previous, tip) {
				return nil, fmt.Errorf("working tip %q has conflicting product definitions", tip.ID)
			}
			definitions[tip.ID] = tip
		}
		result[surface] = manifest.UI.Tips
	}
	return result, nil
}

// The manifests are embedded and static, so assemble and conflict-check the
// catalog once and filter it per request instead of re-reading every manifest.
var (
	cachedTipsCatalog     map[string][]agentprofiles.WorkingTip
	cachedTipsCatalogErr  error
	cachedTipsCatalogOnce sync.Once
)

func cachedProductTips() (map[string][]agentprofiles.WorkingTip, error) {
	cachedTipsCatalogOnce.Do(func() {
		cachedTipsCatalog, cachedTipsCatalogErr = productWorkingTips()
	})
	return cachedTipsCatalog, cachedTipsCatalogErr
}

func (api *StreamingAPI) handleProductTips(w http.ResponseWriter, r *http.Request) {
	tips, err := cachedProductTips()
	if err != nil {
		http.Error(w, "Product tips are unavailable", http.StatusInternalServerError)
		return
	}
	claims := GetUserFromContext(r.Context())
	filtered := make(map[string][]agentprofiles.WorkingTip, len(tips))
	for surface, list := range tips {
		if !productpolicy.Enabled(surface) || !userAllowedProduct(claims, surface) {
			continue
		}
		filtered[surface] = list
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"tips": filtered})
}
