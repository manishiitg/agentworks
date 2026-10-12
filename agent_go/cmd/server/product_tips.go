package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/sparkquillproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/videoproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Expose only display metadata; prompts, tool policies and dependencies stay server-side.
func productWorkingTips() (map[string][]agentprofiles.WorkingTip, error) {
	loaders := map[string]func() (agentprofiles.ProductManifest, error){
		"agentworks":    agentworksproduct.AgentWorksManifest,
		"relays":        relayproduct.Manifest,
		"code":          codeproduct.CodeManifest,
		"work":          workproduct.WorkManifest,
		"video-studio":  videoproduct.VideoStudioManifest,
		"sparkquill":    sparkquillproduct.SparkQuillManifest,
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

func (api *StreamingAPI) handleProductTips(w http.ResponseWriter, r *http.Request) {
	tips, err := productWorkingTips()
	if err != nil {
		http.Error(w, "Product tips are unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"tips": tips})
}
