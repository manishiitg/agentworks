package codexcli

import (
	"reflect"
	"testing"
)

func TestCodexSubmitKeysUseDoubleEnter(t *testing.T) {
	want := []string{"Enter", "Enter"}
	if got := codexSubmitKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("codex submit keys = %v, want %v", got, want)
	}
}
