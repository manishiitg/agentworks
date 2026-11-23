package picli

import (
	"encoding/json"
	"testing"
)

func TestPiGenericBridgeToolName(t *testing.T) {
	cases := map[string]bool{
		"mcp":                        true,
		"mcp_something":              true,
		"api_bridge_execute_command": true,
		"some_mcp_thing":             true,
		"execute_shell_command":      false,
		"":                           false,
	}
	for name, want := range cases {
		if got := piGenericBridgeToolName(name); got != want {
			t.Errorf("piGenericBridgeToolName(%q) = %v, want %v", name, got, want)
		}
	}
}

// PLAT (real pi-cli tool names). Confirmed live against a real session
// transcript: a bridge-routed tool call's arguments look exactly like this.
func TestPiRealToolNameFromBridgeArgs(t *testing.T) {
	args := []byte(`{"tool":"api_bridge_execute_shell_command","args":"{\"command\":\"ls\"}"}`)
	if got := piRealToolNameFromBridgeArgs(args); got != "api_bridge_execute_shell_command" {
		t.Fatalf("piRealToolNameFromBridgeArgs() = %q, want api_bridge_execute_shell_command", got)
	}
}

func TestPiRealToolNameFromBridgeArgsEmpty(t *testing.T) {
	if got := piRealToolNameFromBridgeArgs(nil); got != "" {
		t.Fatalf("piRealToolNameFromBridgeArgs(nil) = %q, want empty", got)
	}
	if got := piRealToolNameFromBridgeArgs([]byte(`{}`)); got != "" {
		t.Fatalf("piRealToolNameFromBridgeArgs({}) = %q, want empty", got)
	}
}

// A large tool argument can push compactForMarker's truncation wrapper to
// fire in the embedded TS extension before this Go code ever sees it -- "tool"
// must still be recoverable from the truncated preview, since pi's bridge
// puts it first. Mirrors compactForMarker's actual wrapper shape: a valid,
// complete outer JSON document whose "preview" string value happens to
// contain an incomplete (truncated) JSON prefix.
func TestPiRealToolNameFromBridgeArgsRecoversFromTruncatedPreview(t *testing.T) {
	innerPrefix := `{"tool":"api_bridge_diff_patch_workspace_file","args":"{\"filepath\":\"very`
	args, err := json.Marshal(map[string]interface{}{
		"mlpMarkerTruncated": true,
		"preview":            innerPrefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := piRealToolNameFromBridgeArgs(args); got != "api_bridge_diff_patch_workspace_file" {
		t.Fatalf("piRealToolNameFromBridgeArgs(truncated) = %q, want api_bridge_diff_patch_workspace_file", got)
	}
}

func TestPiResultTextFromMarker(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain string", `"file written successfully"`, "file written successfully"},
		{"empty", ``, ""},
		{"null", `null`, ""},
		{"object falls back to raw JSON", `{"ok":true,"lines":42}`, `{"ok":true,"lines":42}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := piResultTextFromMarker([]byte(tc.input)); got != tc.want {
				t.Fatalf("piResultTextFromMarker(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPiExtractStringField(t *testing.T) {
	text := `{"tool":"api_bridge_execute_shell_command","args":"{\"command\":\"very long truncated`
	if got := piExtractStringField(text, "tool"); got != "api_bridge_execute_shell_command" {
		t.Fatalf("piExtractStringField() = %q, want api_bridge_execute_shell_command", got)
	}
	if got := piExtractStringField(`{"other":"value"}`, "tool"); got != "" {
		t.Fatalf("piExtractStringField() missing field = %q, want empty", got)
	}
}
