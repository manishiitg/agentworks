package server

import (
	"reflect"
	"testing"
)

// A secret name that Vault permits but that has no stored value is not loaded, so it never appears in a Builder's
// secret list. It must be reported separately, or the Builder cannot say why the secret cannot be switched on.
func TestSecretNamesWithoutValue(t *testing.T) {
	allowed := map[string]bool{"GEMINI_API_KEY": true, "FAL_AI_KEY": true, "EMPTY_KEY": true}
	loaded := []globalSecretEntry{{Name: "GEMINI_API_KEY", Value: "x"}, {Name: "EMPTY_KEY", Value: "  "}, {Name: "NOT_PERMITTED", Value: "y"}}
	got := secretNamesWithoutValue(allowed, loaded)
	if want := []string{"EMPTY_KEY", "FAL_AI_KEY"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names without a value = %v, want %v", got, want)
	}
	if got := secretNamesWithoutValue(map[string]bool{}, loaded); len(got) != 0 {
		t.Fatalf("nothing is permitted, got %v", got)
	}
}
