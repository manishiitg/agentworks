package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func knowledgebaseExecute(ctx context.Context, userID string, accessOnly bool, tool string, args map[string]any) (string, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil {
		claims = principalClaims(userID)
	}
	if strings.TrimSpace(userID) == "" || claims.UserID != userID || !knowledgebaseProductAllowed(claims) {
		return "", fmt.Errorf("Knowledge Base is unavailable to this principal")
	}
	if claims.AccessToken != nil {
		store, err := openAccessTokens()
		if err != nil {
			return "", fmt.Errorf("token authority unavailable")
		}
		fresh, err := store.Active(ctx, claims.AccessToken.ID, time.Now())
		_ = store.Close()
		if err != nil {
			return "", fmt.Errorf("connection is expired or revoked")
		}
		copy := *claims
		copy.AccessToken = &fresh
		claims = &copy
		var def externalTool
		for _, candidate := range knowledgebase.ToolDefinitions() {
			if candidate.Name == tool {
				def = externalTool{Name: tool, mutates: candidate.Mutates}
				break
			}
		}
		if accessOnly || !externalTokenAllows(claims, def) {
			return "", fmt.Errorf("connection does not allow this operation")
		}
	}
	service, err := knowledgebaseService()
	if err != nil {
		return "", fmt.Errorf("Knowledge Base service unavailable")
	}
	if err = knowledgebaseSyncIdentities(ctx, service); err != nil {
		return "", err
	}
	r := (&http.Request{}).WithContext(context.WithValue(ctx, UserContextKey, claims))
	p := knowledgebasePrincipal(r, claims)
	p.AccessOnly = accessOnly
	result, err := service.Call(ctx, p, tool, args)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func knowledgebaseAccessExecutor(ctx context.Context, runtime agentprofiles.ToolRuntimeContext, args map[string]any) (string, error) {
	if runtime.Product != "knowledgebase" {
		return "", fmt.Errorf("access tool requires the Knowledge Base profile")
	}
	return knowledgebaseExecute(ctx, runtime.UserID, true, "manage_knowledgebase_access", args)
}

// Content operations use the same domain boundary in Crews, Code and workflows.
// The dedicated access builder's allowlist excludes every one of these tools.
func createKnowledgebaseTools(userID string) ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	var tools []llmtypes.Tool
	executors := map[string]interface{}{}
	categories := map[string]string{}
	if !productEnabled("knowledgebase") {
		return tools, executors, categories
	}
	for _, def := range knowledgebase.ToolDefinitions() {
		if def.Name == "manage_knowledgebase_access" {
			continue
		}
		def := def
		encoded, _ := json.Marshal(def.InputSchema)
		params := new(llmtypes.Parameters)
		if err := json.Unmarshal(encoded, params); err != nil {
			continue
		}
		tools = append(tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: def.Name, Description: def.Description, Parameters: params}})
		executors[def.Name] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			return knowledgebaseExecute(ctx, userID, false, def.Name, args)
		}
		categories[def.Name] = "knowledgebase"
	}
	return tools, executors, categories
}
