package server

import "encoding/json"

// Vault access has two levels (owner, 2026-10-09). A person is a Vault
// manager (admin with Vault) or a Vault reader (the vault_reader flag with
// Vault); a connection holds vault:manage or vault:read. A call gets the
// lower of the two, rechecked on every call. Readers inspect, list, read the
// audit log and run SQL queries; they change nothing and never call a Vault
// tool as the administrator.
const (
	vaultNone = iota
	vaultRead
	vaultManage
)

func vaultReaderActive(userID string) bool {
	claims := &UserClaims{UserID: userID}
	access := userAccessForClaims(claims)
	return userID != "" && access.VaultReader && !access.Disabled && userAllowedProduct(claims, "mcp-gateway")
}

func vaultPersonLevel(userID string) int {
	switch {
	case vaultAdminActive(userID):
		return vaultManage
	case vaultReaderActive(userID):
		return vaultRead
	}
	return vaultNone
}

func vaultClaimsLevel(c *UserClaims) int {
	if c == nil {
		return vaultNone
	}
	level := vaultPersonLevel(c.UserID)
	if t := c.AccessToken; t != nil {
		limit := vaultNone
		if t.Allows("vault:manage") {
			limit = vaultManage
		} else if t.Allows("vault:read") {
			limit = vaultRead
		}
		level = min(level, limit)
	}
	return level
}

var vaultReadOperations = map[string]map[string]bool{
	"manage_vault_access":        {"inspect_environment": true, "list_users": true, "inspect_group": true, "inspect_user": true, "inspect_tool": true, "connection_status": true},
	"manage_vault_groups":        {"list": true, "list_members": true},
	"manage_vault_secret_access": {"list": true},
	"manage_vault_tools":         {"list": true, "versions": true},
}

// vaultToolHasReads: the tool has at least one operation a reader may call.
func vaultToolHasReads(name string) bool {
	return name == "query_vault_db" || name == "read_vault_audit" || vaultReadOperations[name] != nil
}

func vaultOperationReads(name string, args map[string]any) bool {
	if name == "query_vault_db" || name == "read_vault_audit" {
		return true
	}
	return vaultReadOperations[name][externalArg(args, "operation")]
}

// vaultReadAgentRequest: a gateway setup or database request a reader may make.
func vaultReadAgentRequest(path string, payload json.RawMessage) bool {
	if path == "/api/admin/database/query" {
		return true
	}
	var in struct {
		Operation string `json:"operation"`
	}
	return path == "/api/admin/setup/tool" && json.Unmarshal(payload, &in) == nil && vaultReadOperations["manage_vault_access"][in.Operation]
}
