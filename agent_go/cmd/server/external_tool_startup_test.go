package server

import "testing"

// The real startup registry parses embedded Builder schemas as well as the
// public catalog. A malformed schema must fail here before service startup.
func TestExternalToolCatalogInitializes(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) == 0 {
		t.Fatal("startup tool catalog is empty")
	}
}
