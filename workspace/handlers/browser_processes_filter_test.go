package handlers

import (
	"reflect"
	"testing"
)

func TestFilterBrowserPIDsKeepsOnlyBrowserProcesses(t *testing.T) {
	current := []BrowserProcess{{PID: 4100}, {PID: 4101}}
	got := filterBrowserPIDs([]int{1, 4100, 999, 4101, 0, -5, 4100}, current)
	if want := []int{4100, 4101, 4100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (duplicates of a real browser pid are harmless)", got, want)
	}
	if got := filterBrowserPIDs([]int{1, 2, 3}, nil); len(got) != 0 {
		t.Fatalf("with no browsers nothing may be killed, got %v", got)
	}
}
