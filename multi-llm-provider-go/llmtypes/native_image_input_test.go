package llmtypes

import (
	"context"
	"testing"
)

// PLAT-746-1000: an admitted attachment must not become a child/other chat's
// image capability just because that turn reused its parent's Go context.
func TestNativeImageInputCapabilityIsScopedToTheAdmittedSession(t *testing.T) {
	inputs := []NativeImageInput{{ID: "upload"}}
	ctx := WithNativeImageInputs(context.Background(), "chat", inputs)
	inputs[0].ID = "changed"
	for _, other := range []string{"", "child", "other-chat"} {
		if len(NativeImageInputsForSession(ctx, other)) != 0 {
			t.Fatal("attachment crossed session boundary", other)
		}
	}
	got := NativeImageInputsForSession(ctx, "chat")
	if len(got) != 1 || got[0].ID != "upload" {
		t.Fatal("admitted attachment was lost or changed")
	}
	got[0].ID = "changed"
	if NativeImageInputsForSession(ctx, "chat")[0].ID != "upload" {
		t.Fatal("consumer changed another delivery's image binding")
	}
}
