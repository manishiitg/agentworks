package pathidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryIdentityAliasesAndDistinctPaths(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agentworks")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if !Same(dir, link) || Key(dir) != Key(link) {
		t.Fatal("symlink did not retain directory identity")
	}
	alias := filepath.Join(root, "AgentWorks")
	if _, err := os.Stat(alias); err == nil {
		if !Same(dir, alias) || Key(dir) != Key(alias) {
			t.Fatal("case alias did not retain physical identity")
		}
	} else {
		if err := os.Mkdir(alias, 0700); err != nil {
			t.Fatal(err)
		}
		if Same(dir, alias) || Key(dir) == Key(alias) {
			t.Fatal("distinct case-sensitive directories collapsed")
		}
	}
	other := t.TempDir()
	if Same(dir, other) || Same("", "") {
		t.Fatal("unrelated or empty paths considered identical")
	}
	if _, err := Resolve(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing path resolved")
	}
	if Same(filepath.Join(dir, "missing"), filepath.Join(dir, "MISSING")) {
		t.Fatal("missing path case aliases inferred without filesystem evidence")
	}
}
