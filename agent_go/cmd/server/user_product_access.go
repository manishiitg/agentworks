package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"os"
	"strings"
)

// Per-user product/workflow access grants, layered on top of the existing
// per-workflow read/write/owner grants in workflow_permissions.go. A user
// with NO entry here is unrestricted -- this file only ever narrows access
// for users explicitly listed, so every other deployment (desktop,
// single-user servers, multi-user servers that never write this file) keeps
// today's unrestricted behavior unchanged.
//
// Storage: config/user-product-access.json, shape:
//
//	{
//	  "owner": { "products": ["video-studio", "agentworks"], "workflow_ids": ["demo-workflow"] },
//	  "reader":   { "products": ["video-studio"] }
//	}
//
// Keys are normalized (lowercase, trimmed) and matched against UserID,
// Username, or Email, same precedence and normalization as
// workflowAccessForIdentity in workflow_permissions.go. There is no admin
// CRUD for this file yet -- it's written once via the workspace API during
// deployment; add upsert/delete handlers here if the user list grows enough
// to need one.

type UserProductAccess struct {
	Products    []string `json:"products,omitempty"`
	WorkflowIDs []string `json:"workflow_ids,omitempty"`
}

func userProductAccessFilePath() string {
	return "config/user-product-access.json"
}

func readUserProductAccessFile() (map[string]UserProductAccess, error) {
	data, exists, err := readFileFromWorkspace(context.Background(), userProductAccessFilePath())
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]UserProductAccess{}, nil
	}
	var raw map[string]UserProductAccess
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	out := make(map[string]UserProductAccess, len(raw))
	for k, v := range raw {
		key := normalizeWorkflowPermissionKey(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out, nil
}

func userProductAccessForIdentity(userID, username, email string) (UserProductAccess, bool) {
	entries, err := readUserProductAccessFile()
	if err != nil || len(entries) == 0 {
		return UserProductAccess{}, false
	}
	for _, key := range []string{userID, username, email} {
		normalized := normalizeWorkflowPermissionKey(key)
		if normalized == "" {
			continue
		}
		if access, ok := entries[normalized]; ok {
			return access, true
		}
	}
	return UserProductAccess{}, false
}

func userProductAccessForClaims(claims *UserClaims) (UserProductAccess, bool) {
	if claims == nil {
		return UserProductAccess{}, false
	}
	return userProductAccessForIdentity(claims.UserID, claims.Username, claims.Email)
}

// userProductPolicy is the one answer to "which products may this identity
// open". The user directory wins when it knows the identity (see
// accessForRecord: admins and members with no list are unrestricted, a
// read-only account with no list gets NONE, an explicit list restricts to
// it). Otherwise the legacy config/user-product-access.json applies, where
// an absent entry or empty list means unrestricted. restricted=false means
// "all products"; restricted=true with an empty list means "no products".
func userProductPolicy(userID, username, email string) (products []string, restricted bool) {
	if rec := directoryUserFor(userID, username, email); rec != nil {
		acc := accessForRecord(rec)
		return acc.Products, acc.ProductsRestricted
	}
	access, ok := userProductAccessForIdentity(userID, username, email)
	if !ok || len(access.Products) == 0 {
		return nil, false
	}
	return access.Products, true
}

// userAllowedProduct reports whether claims may use the given product
// surface (see userProductPolicy). An empty product name (the generic,
// profile-less AgentWorks chat path has no product) is always allowed here
// -- workflow-level access is what actually gates that path, via
// userAllowedWorkflowID.
func userAllowedProduct(claims *UserClaims, product string) bool {
	product = strings.TrimSpace(product)
	if !productpolicy.Enabled(product) {
		return false
	}
	if product == "" || claims == nil {
		return true
	}
	if claims.Provider == "bot_route" {
		return claims.BotRouteProfileID != "" && strings.EqualFold(product, claims.BotRouteProfileID)
	}
	if adminOnlyProduct(product) && !userAccessForClaims(claims).Admin {
		return false
	}
	if productAvailableToAll(product) {
		return true
	}
	products, restricted := userProductPolicy(claims.UserID, claims.Username, claims.Email)
	if !restricted {
		return true
	}
	for _, allowed := range products {
		if strings.EqualFold(strings.TrimSpace(allowed), product) {
			return true
		}
	}
	return false
}

// productAvailableToAll provides a deployment-owned baseline product set.
// It is useful for a product backed entirely by per-user storage: even a
// read-only AgentWorks account can use that product without gaining permission
// to create or edit shared workflows.
func productAvailableToAll(product string) bool {
	for _, candidate := range strings.Split(os.Getenv("AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL"), ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(product)) {
			return true
		}
	}
	return false
}

func appendProductsAvailableToAll(products []string) []string {
	result := append([]string(nil), products...)
	for _, candidate := range strings.Split(os.Getenv("AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		found := false
		for _, product := range result {
			if strings.EqualFold(strings.TrimSpace(product), candidate) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, candidate)
		}
	}
	return result
}

// adminOnlyProduct provides a deployment-wide authorization boundary for
// products that are installed but not yet ready for general access. This is
// deliberately enforced in the server as well as reflected in /api/auth/me;
// hiding a product in the switcher alone would leave its APIs reachable.
func adminOnlyProduct(product string) bool {
	for _, candidate := range strings.Split(os.Getenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES"), ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(product)) {
			return true
		}
	}
	return false
}

func filterAdminOnlyProducts(products []string) []string {
	filtered := make([]string, 0, len(products))
	for _, product := range products {
		if !adminOnlyProduct(product) {
			filtered = append(filtered, product)
		}
	}
	return filtered
}

// userAllowedWorkflowID reports whether claims may see/run the given
// workflow. A user with no explicit entry, or an entry with an empty
// WorkflowIDs list, is unrestricted.
func userAllowedWorkflowID(claims *UserClaims, workflowID string) bool {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return true
	}
	access, ok := userProductAccessForClaims(claims)
	if !ok || len(access.WorkflowIDs) == 0 {
		return true
	}
	for _, allowed := range access.WorkflowIDs {
		if strings.EqualFold(strings.TrimSpace(allowed), workflowID) {
			return true
		}
	}
	return false
}

// filterWorkflowManifestsForUser narrows a discovered-workflows list to the
// ones claims is allowed to see. An unrestricted user (no entry, or an entry
// with an empty WorkflowIDs list) gets the list back unchanged, including a
// nil input turning into a nil/empty output rather than panicking.
func filterWorkflowManifestsForUser(claims *UserClaims, discovered []DiscoveredWorkflow) []DiscoveredWorkflow {
	filtered := make([]DiscoveredWorkflow, 0, len(discovered))
	for _, workflow := range discovered {
		if workflow.Manifest == nil {
			continue
		}
		if !userAllowedWorkflowID(claims, workflow.Manifest.ID) {
			continue
		}
		level := workflowAccessForManifest(claims, workflow.Manifest)
		if level == WorkflowAccessNone {
			continue
		}
		workflow.MyAccess = level
		filtered = append(filtered, workflow)
	}
	return filtered
}

// productAccessResponseFields is merged into GET /api/auth/me the same way
// workflowPermissionResponseFields already is. allowed_products/
// allowed_workflow_ids are omitted (null) when the user has no explicit
// entry, meaning "unrestricted" -- the frontend treats null the same as an
// empty allowlist would be wrong to treat it as.
func productAccessResponseFields(claims *UserClaims) map[string]interface{} {
	access, ok := userProductAccessForClaims(claims)
	out := map[string]interface{}{
		"allowed_products":     nil,
		"allowed_workflow_ids": nil,
	}
	if claims != nil {
		access := userAccessForClaims(claims)
		products, restricted := userProductPolicy(claims.UserID, claims.Username, claims.Email)
		if restricted {
			products = appendProductsAvailableToAll(products)
		}
		if !access.Admin && strings.TrimSpace(os.Getenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES")) != "" {
			if !restricted {
				products = knownProductIDs()
			}
			products = filterAdminOnlyProducts(products)
			restricted = true
		}
		if restricted {
			if products == nil {
				products = []string{}
			}
			out["allowed_products"] = products
		}
	}
	if ok && len(access.WorkflowIDs) > 0 {
		out["allowed_workflow_ids"] = access.WorkflowIDs
	}
	return out
}
