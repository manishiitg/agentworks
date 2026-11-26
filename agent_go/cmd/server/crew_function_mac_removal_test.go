package server

import (
	"strings"
	"testing"
)

// #4078: removing a Mac project's functions must keep caller bindings (records with input_mapping, which are not
// functions); the whole record is backed up before the write.
func TestMacFunctionRemovalKeepsBindings(t *testing.T) {
	kept, removed, err := withoutFunctions(`{"functions":[{"id":"f1","name":"trending"},{"id":"b1","name":"daily","input_mapping":{"topic":"x"}}]}`)
	if err != nil || len(removed) != 1 || removed[0] != "trending" {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if strings.Contains(kept, "trending") || !strings.Contains(kept, "input_mapping") {
		t.Fatalf("wrong records kept: %s", kept)
	}
}
