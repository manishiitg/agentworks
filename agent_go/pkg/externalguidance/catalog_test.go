package externalguidance

import (
	"testing"
	"testing/fstest"
)

func TestExposureRequiresValidOptIn(t *testing.T) {
	files := fstest.MapFS{
		"private.md": {Data: []byte("# Internal only\n")},
		"public.md":  {Data: []byte("---\nname: public\ndescription: Public reference\nexternal: true\n---\n# Public\n")},
	}
	topics, err := Load(files, "*.md")
	if err != nil || len(topics) != 1 || topics[0].Name != "public" || topics[0].Content != "# Public\n" {
		t.Fatalf("exposure/body boundary: %v %v", topics, err)
	}
	files["malformed.md"] = &fstest.MapFile{Data: []byte("---\nexternal: true\n---\nBody\n")}
	if _, err := Load(files, "*.md"); err == nil {
		t.Fatal("malformed marked file must fail closed")
	}
}
