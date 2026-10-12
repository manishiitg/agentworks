package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUsageControlsRequirePersonTerminal(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, action := range []string{"on", "off"} {
		var out, err bytes.Buffer
		code := run(context.Background(), []string{"usage", action}, strings.NewReader(""), &out, &err, func(string) string { return "" })
		if code == 0 || !strings.Contains(err.String(), "person commands") {
			t.Fatalf("agent/noninteractive %s control accepted: %d %s", action, code, err.String())
		}
	}
}
