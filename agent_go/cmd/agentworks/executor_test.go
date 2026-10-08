package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutorShellRequiresWritableAlias(t *testing.T) {
	for _, args := range [][]string{{"executor", "connect", "--device", "laptop", "--shell", "missing"}, {"executor", "connect", "--device", "laptop", "--folder", "project=" + t.TempDir(), "--shell", "project"}} {
		var stdout, stderr bytes.Buffer
		args = append([]string{"--config", filepath.Join(t.TempDir(), "executor.json")}, args...)
		code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
		if code == 0 || !strings.Contains(stderr.String(), "requires a matching --write-folder") {
			t.Fatalf("shell granted without writable alias %d %s", code, stderr.String())
		}
	}
}
