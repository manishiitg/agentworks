// Package productpolicy is the installation capability contract shared by
// startup, builder identities, workflow agents and external tool transports.
// Authentication/SSO and listen addresses never select installation capabilities.
package productpolicy

import (
	"context"
	"os"
	"strings"
)

func Local() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_DEPLOYMENT_MODE")), "local")
}
func LocalServerProducts() bool {
	return Local() && strings.TrimSpace(os.Getenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS")) == "1"
}
func Canonical(product string) string {
	switch product = strings.ToLower(strings.TrimSpace(product)); product {
	case "caplayer", "vault":
		return "mcp-gateway"
	case "brain":
		return "knowledgebase"
	default:
		return product
	}
}
func ServerOnly(product string) bool {
	switch Canonical(product) {
	case "relays", "knowledgebase", "mcp-gateway", "llm-gateway":
		return true
	default:
		return false
	}
}
func InstallationAvailable(product string) bool {
	return !Local() || LocalServerProducts() || !ServerOnly(product)
}
func Contains(list, product string) bool {
	for _, candidate := range strings.Split(list, ",") {
		if Canonical(candidate) == Canonical(product) {
			return true
		}
	}
	return false
}

// Enabled intersects the installation's product set with its explicit surface
// and runtime allowlists. Brain/Vault retain the shared-server baseline when
// AGENT_PRODUCTS alone is set; an explicit surface list can exclude them.
func Enabled(product string) bool {
	product = Canonical(product)
	if product == "" {
		return true
	}
	if !InstallationAvailable(product) {
		return false
	}
	if surfaces := strings.TrimSpace(os.Getenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES")); surfaces != "" {
		// Old local runtime-config files can contain only server products. The
		// launcher/UI fallback is the ordinary local product set in that case;
		// builders must use the identical fallback rather than an empty set.
		if Local() && !LocalServerProducts() {
			available := false
			for _, candidate := range strings.Split(surfaces, ",") {
				available = available || InstallationAvailable(candidate)
			}
			if !available {
				surfaces = "agentworks,work,code"
			}
		}
		if !Contains(surfaces, product) {
			return false
		}
	}
	configured := strings.TrimSpace(os.Getenv("AGENT_PRODUCTS"))
	if configured == "" {
		return true
	}
	if !Contains(configured, "sparkquill") && (product == "mcp-gateway" || product == "knowledgebase") {
		return true
	}
	return Contains(configured, product)
}

// Selection can narrow installation capabilities to a caller's current product
// permissions. It cannot enable an uninstalled product. The function remains
// live so retained definitions recheck current authority on execution.
type Selection struct{ Allowed func(string) bool }

func (s Selection) Has(product string) bool {
	product = Canonical(product)
	return Enabled(product) && (s.Allowed == nil || s.Allowed(product))
}

type selectionKey struct{}

func WithSelection(ctx context.Context, s Selection) context.Context {
	return context.WithValue(ctx, selectionKey{}, s)
}
func FromContext(ctx context.Context) Selection {
	if s, ok := ctx.Value(selectionKey{}).(Selection); ok {
		return s
	}
	return Selection{}
}

// ToolProduct identifies platform-owned capabilities only. Private MCP tools
// are not classified by a name substring and keep their existing authorization.
func ToolProduct(name string) string {
	switch name {
	case "brain_browse", "brain_read", "brain_update", "brain_skills", "brain_access", "brain_schedule", "brain_secrets", "brain_backup", "manage_brain_schedule", "manage_brain_secrets", "browse_knowledgebase", "read_knowledgebase", "update_knowledgebase", "backup_knowledgebase", "knowledgebase_skills", "manage_knowledgebase_access":
		return "knowledgebase"
	case "manage_vault_access", "manage_vault_groups", "manage_vault_secret_access", "query_vault_db", "mutate_vault_db", "list_vault_mcp_servers", "call_vault_mcp_tool", "manage_vault_tools", "read_vault_audit", "manage_my_vaults", "manage_global_secret", "manage_vault_secret", "share_mcp_to_vault":
		return "mcp-gateway"
	case "create_relay", "update_relay", "test_relay", "publish_relay", "run_relay", "get_relay_run", "get_relay_releases", "inspect_relay_graph":
		return "relays"
	default:
		return ""
	}
}
func (s Selection) AllowsTool(name string) bool { p := ToolProduct(name); return p == "" || s.Has(p) }

// BindingProduct refers to trusted product.yaml tool-factory namespaces,
// rather than external MCP names supplied by a private connection.
func BindingProduct(id string) string {
	for namespace, product := range map[string]string{"caplayer.": "mcp-gateway", "knowledgebase.": "knowledgebase", "relay.": "relays"} {
		if strings.HasPrefix(id, namespace) {
			return product
		}
	}
	return ""
}

func (s Selection) AllowsBinding(id string) bool {
	p := BindingProduct(id)
	return p == "" || s.Has(p)
}
func SkillProduct(name string) string {
	switch name {
	case "brain", "brain-builder", "knowledgebase-builder":
		return "knowledgebase"
	case "vault-access", "caplayer-access":
		return "mcp-gateway"
	case "relay-builder", "relay-dashboard":
		return "relays"
	default:
		return ""
	}
}
func (s Selection) AllowsSkill(name string) bool { p := SkillProduct(name); return p == "" || s.Has(p) }
