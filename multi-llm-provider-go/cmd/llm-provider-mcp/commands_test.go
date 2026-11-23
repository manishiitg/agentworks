package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentmodels"
)

func TestModelsCommandPrintsPiCatalogAsJSON(t *testing.T) {
	command := newModelsCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"pi-cli", "--json"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var catalogs []codingagentmodels.Catalog
	if err := json.Unmarshal(output.Bytes(), &catalogs); err != nil {
		t.Fatalf("JSON output error = %v: %s", err, output.String())
	}
	if len(catalogs) != 1 || catalogs[0].Provider != "pi-cli" || len(catalogs[0].Models) == 0 {
		t.Fatalf("catalogs = %#v", catalogs)
	}
}

func TestRootCommandIncludesManagementCommands(t *testing.T) {
	root := newRootCommand()
	for _, name := range []string{"setup", "doctor", "models", "uninstall"} {
		if command, _, err := root.Find([]string{name}); err != nil || command.Name() != name {
			t.Fatalf("command %q was not registered", name)
		}
	}
}
