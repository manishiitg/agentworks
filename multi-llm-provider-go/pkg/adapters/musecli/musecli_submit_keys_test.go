package musecli

import (
	"reflect"
	"testing"
)

func TestMuseSubmitKeysUseDoubleEnter(t *testing.T) {
	want := []string{"Enter", "Enter"}
	if got := museSubmitKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("muse submit keys = %v, want %v", got, want)
	}
}
