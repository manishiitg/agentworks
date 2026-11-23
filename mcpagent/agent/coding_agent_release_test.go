package mcpagent

import (
	"reflect"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAppendManagedCodingAgentReleaseSession(t *testing.T) {
	tests := []struct {
		name       string
		managedBin string
		sessionID  string
		wantPinned bool
	}{
		{name: "application chat", managedBin: "/managed/bin", sessionID: "chat-123", wantPinned: true},
		{name: "manager disabled", sessionID: "chat-123"},
		{name: "global agent", managedBin: "/managed/bin", sessionID: "global"},
		{name: "missing session", managedBin: "/managed/bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOpts := appendManagedCodingAgentReleaseSession(nil, tt.managedBin, tt.sessionID)
			if got := len(gotOpts); got != btoi(tt.wantPinned) {
				t.Fatalf("option count = %d, want %d", got, btoi(tt.wantPinned))
			}
			if !tt.wantPinned {
				return
			}

			got := &llmtypes.CallOptions{}
			gotOpts[0](got)
			want := &llmtypes.CallOptions{}
			llmtypes.WithCodingAgentReleaseSession(tt.sessionID)(want)
			if !reflect.DeepEqual(got.Metadata, want.Metadata) {
				t.Fatalf("release metadata = %#v, want %#v", got.Metadata, want.Metadata)
			}
		})
	}
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}
