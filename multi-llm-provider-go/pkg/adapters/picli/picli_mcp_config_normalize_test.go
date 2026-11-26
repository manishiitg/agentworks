package picli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizePiMCPConfigNativeDirectExposure(t *testing.T) {
	out, err := normalizePiMCPConfig(`{"settings":{"disableProxyTool":false},"mcpServers":{"api-bridge":{"command":"mcpbridge","directTools":true,"lifecycle":"keep-alive","env":{"TOKEN":"session-token"},"timeout":123},"docs":{"url":"https://example.invalid/mcp","exposure":"hidden"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["autoEnableCodemode"] != false {
		t.Fatalf("codemode unexpectedly enabled: %s", out)
	}
	servers := got["mcpServers"].(map[string]interface{})
	bridge := servers["api-bridge"].(map[string]interface{})
	if bridge["exposure"] != "direct" || bridge["timeout"] != float64(123) || bridge["env"].(map[string]interface{})["TOKEN"] != "session-token" {
		t.Fatalf("native settings or credentials lost: %s", out)
	}
	if servers["docs"].(map[string]interface{})["exposure"] != "hidden" {
		t.Fatalf("explicit exposure lost: %s", out)
	}
	for _, field := range []string{"directTools", "lifecycle", "disableProxyTool", "settings"} {
		if strings.Contains(string(out), `"`+field+`"`) {
			t.Fatalf("retired plugin setting %s survives: %s", field, out)
		}
	}
}

func TestNormalizePiMCPConfigRejectsInvalidServers(t *testing.T) {
	for _, input := range []string{`{`, `{}`, `{"mcpServers":{}}`, `{"mcpServers":{"api-bridge":"invalid"}}`} {
		if _, err := normalizePiMCPConfig(input); err == nil {
			t.Fatalf("accepted invalid config %s", input)
		}
	}
}
