package cursorcli

import "testing"

func TestCursorStreamNarrationRejectsPartialStreams(t *testing.T) {
	for _, tc := range []struct {
		name         string
		order, texts []string
		valid        bool
	}{
		{"all narration", []string{"text", "tool", "text", "tool"}, []string{"FIRST", "SECOND"}, true},
		{"tools only", []string{"tool", "tool"}, nil, false},
		{"final only", []string{"tool", "tool", "text"}, []string{"FIRST SECOND"}, false},
		{"second missing", []string{"text", "tool", "tool"}, []string{"FIRST"}, false},
		{"duplicate", []string{"text", "text", "tool", "text", "tool"}, []string{"FIRST", "FIRST", "SECOND"}, false},
		{"duplicate in chunk", []string{"text", "tool", "text", "tool"}, []string{"FIRST FIRST", "SECOND"}, false},
		{"out of order", []string{"text", "tool", "text", "tool"}, []string{"SECOND", "FIRST"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := assertCursorStreamNarration(cursorStreamCapture{order: tc.order, contentTexts: tc.texts}, []string{"FIRST", "SECOND"})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestCursorStreamNarrationAllowsSchemaDiscovery(t *testing.T) {
	capture := cursorStreamCapture{order: []string{"text", "tool", "text", "tool", "text", "tool"}, contentTexts: []string{"Inspecting schemas", "FIRST", "SECOND"}, toolNames: []string{"GetDynamicTools", "CallDynamicTool", "CallDynamicTool"}}
	if err := assertCursorStreamNarration(capture, []string{"FIRST", "SECOND"}); err != nil {
		t.Fatal(err)
	}
}
