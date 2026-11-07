package chatvisuals

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-750-89: the formatter must not inherit the server's secret environment.
func TestFormatterChildHasNoServerCredentials(t *testing.T) {
	bin := t.TempDir()
	probe := `#!/bin/sh
if [ "${SECRET_RENDER_TEST+x}" = x ] || [ "${OPENAI_API_KEY+x}" = x ]; then
  printf '%s' '{"error":"inherited_credentials"}'
else
  printf '%s' '{"blocks":[{"type":"section"}],"text":"environment empty"}'
fi
`
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte(probe), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRET_RENDER_TEST", "test-marker")
	t.Setenv("OPENAI_API_KEY", "test-marker")
	t.Setenv("PATH", bin)
	result, err := Format(context.Background(), "test", true)
	if err != nil || result.Text != "environment empty" {
		t.Fatalf("formatter inherited credentials: %v", err)
	}
}
