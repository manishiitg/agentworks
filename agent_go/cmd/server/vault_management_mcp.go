package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// Administrative setup is explicit and separate from group-scoped runtime use.
// Both the Vault builder and vault:manage MCP adapter call this implementation.
func vaultManagementMCP(ctx context.Context, person, operation string, args map[string]any) (string, error) {
	if !vaultBuilderAdministrator(person) {
		return "", fmt.Errorf("Vault administrator required")
	}
	data, err := vaultRuntimeRequest(ctx, person, "/api/admin/runtime/builder/servers")
	if err != nil {
		return "", err
	}
	var inventory vaultAccessInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return "", fmt.Errorf("invalid Vault inventory")
	}
	for i := range inventory.Servers {
		inventory.Servers[i].Name = vaultServerName(inventory.Servers[i].ID)
	}
	for i := range inventory.Groups {
		for j := range inventory.Groups[i].Servers {
			inventory.Groups[i].Servers[j].Name = vaultServerName(inventory.Groups[i].Servers[j].ID)
		}
	}
	inventory.Users, err = vaultDirectoryUsers(&UserClaims{UserID: person})
	if err != nil {
		return "", err
	}
	if operation == "list_vault_mcp_servers" {
		out, err := json.Marshal(map[string]any{"servers": inventory.Servers, "groups": inventory.Groups, "secrets": inventory.Secrets, "users": inventory.Users})
		return string(out), err
	}
	server, tool := externalArg(args, "server"), externalArg(args, "tool")
	arguments, ok := args["arguments"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("arguments object required")
	}
	connector := ""
	for _, row := range inventory.Servers {
		if row.Name != server {
			continue
		}
		for _, definition := range row.Tools {
			if definition.Name == tool {
				connector = row.ID
				break
			}
		}
	}
	if connector == "" {
		return "", fmt.Errorf("exact active Vault connection and approved tool required; inspect list_vault_mcp_servers")
	}
	target, secret, err := capLayerServiceConfig()
	if err != nil {
		return "", fmt.Errorf("Vault unavailable")
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/api/admin/runtime/builder/mcp"
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	httpClient := &http.Client{Transport: capLayerTransport, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	headers := map[string]string{"Authorization": "Bearer " + secret, "X-CapLayer-Actor": person, "X-Vault-Platform-User": "1", "X-Vault-Connector": connector, "X-Vault-Builder": "1"}
	// Recheck the live account before every transport request, including callTool
	// after initialize. Never cache an administrative grant in an MCP session.
	headerFunc := func(context.Context) map[string]string {
		if !vaultBuilderAdministrator(person) {
			return map[string]string{}
		}
		return headers
	}
	wire, err := transport.NewStreamableHTTP(target.String(), transport.WithHTTPBasicClient(httpClient), transport.WithHTTPHeaderFunc(headerFunc))
	if err != nil {
		return "", err
	}
	cli := client.NewClient(wire)
	defer cli.Close()
	if err := cli.Start(ctx); err != nil {
		return "", fmt.Errorf("Vault MCP unavailable")
	}
	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: "2024-11-05", ClientInfo: mcp.Implementation{Name: "agentworks-vault-management", Version: "1"}}}); err != nil {
		return "", fmt.Errorf("Vault MCP initialization failed: %w", err)
	}
	if !vaultBuilderAdministrator(person) {
		return "", fmt.Errorf("Vault administrator access changed")
	}
	request := mcp.CallToolRequest{}
	request.Params.Name, request.Params.Arguments = tool, arguments
	result, err := cli.CallTool(ctx, request)
	if err != nil {
		return "", fmt.Errorf("Vault MCP call failed: %w", err)
	}
	if result.IsError {
		return "", fmt.Errorf("Vault MCP call failed: %v", result.Content)
	}
	out, err := json.Marshal(result)
	return string(out), err
}
