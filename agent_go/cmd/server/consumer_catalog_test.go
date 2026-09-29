package server

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// Every deployment builds its catalog from the shared one plus an optional
// override (deploy/common/build-mcp-catalog.py). Apply the same merge and
// check each deployment still gets the common integrations.
func TestConsumerCatalogsStayInSync(t *testing.T) {
	base, err := mcpclient.LoadConfig("../../configs/mcp_servers_clean.json", loggerv2.NewNoop())
	if err != nil {
		t.Fatal(err)
	}
	added := []string{"Todoist", "Asana", "ClickUp", "Atlassian", "Dropbox", "Miro", "Figma", "GitHub", "GoogleGmail", "GitLab", "HubSpot", "Stripe", "Supabase", "Zapier"}
	for _, path := range []string{"", "../../../deploy/aws-ec2/server/mcp-servers.override.json", "../../../deploy/rootless-linux/products/confida/mcp-servers.override.json"} {
		catalog := map[string]mcpclient.MCPServerConfig{}
		for name, server := range base.MCPServers {
			catalog[name] = server
		}
		if path != "" {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var override struct {
				MCPServers map[string]*mcpclient.MCPServerConfig `json:"mcpServers"`
			}
			if err := json.Unmarshal(data, &override); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			for name, server := range override.MCPServers {
				if server == nil {
					delete(catalog, name)
				} else {
					catalog[name] = *server
				}
			}
		}
		for _, name := range append([]string{"Notion", "Canva", "Airtable"}, added...) {
			entry, ok := catalog[name]
			if !ok || entry.URL == "" || entry.OAuth == nil || entry.OAuth.AuthURL == "" || entry.OAuth.TokenURL == "" {
				t.Errorf("%s: %s missing endpoint/auth metadata", path, name)
			}
		}
		for _, name := range added {
			if !reflect.DeepEqual(catalog[name], base.MCPServers[name]) {
				t.Errorf("%s: %s differs from shared catalog", path, name)
			}
		}
	}
	if base.MCPServers["Asana"].OAuth.RegistrationEndpoint != "" {
		t.Fatal("Asana v2 must not advertise an invented DCR endpoint")
	}
	for _, name := range []string{"GitHub", "HubSpot"} {
		if base.MCPServers[name].OAuth.RegistrationEndpoint != "" {
			t.Fatalf("%s requires a registered OAuth app; do not advertise DCR", name)
		}
	}
}
