package common

import (
	"reflect"
	"testing"
)

func TestIncludeDefaultVaultToolsPreservesPrivateRestrictions(t *testing.T) {
	original := []string{"private:read", "workspace:read_file"}
	got := IncludeDefaultVaultTools(original, []string{"private", "vault_b__scope_abc", "vault_a__scope_def", "vault_a__scope_def"})
	want := []string{"private:read", "workspace:read_file", "vault_a__scope_def:*", "vault_b__scope_abc:*"}
	if !reflect.DeepEqual(got, want) || len(original) != 2 {
		t.Fatalf("selection: %v; original: %v", got, original)
	}
	if len(IncludeDefaultVaultTools(nil, []string{"vault_a__scope_def"})) != 0 {
		t.Fatal("empty allowlist should remain unrestricted")
	}
}

func TestMigratedVaultToolPicksStayRestricted(t *testing.T) {
	got := RemapMCPToolSelection([]string{"vault_c-12345678:read"}, map[string]string{"vault_c-12345678:read": "vault_c-12345678__scope_abc:example__read"})
	got = IncludeDefaultVaultTools(got, []string{"vault_c-12345678__scope_abc"})
	if !reflect.DeepEqual(got, []string{"vault_c-12345678__scope_abc:example__read"}) {
		t.Fatalf("migrated restriction widened: %v", got)
	}
}
