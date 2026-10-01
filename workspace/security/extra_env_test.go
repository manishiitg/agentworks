package security

import (
	"strings"
	"testing"
)

func TestMergeExtraEnvAppendsAndKeepsTheTempFolderBeforePythonPath(t *testing.T) {
	env := []string{"PATH=/usr/bin", "TMPDIR=/run/tmp123"}
	got := MergeExtraEnv(env, map[string]string{
		"MCP_API_TOKEN": "tok", "SECRET_X": "s", "VAR_Y": "v", "PYTHONPATH": "/platform/helpers",
	})
	have := map[string]string{}
	for _, e := range got {
		k, v, _ := strings.Cut(e, "=")
		have[k] = v
	}
	for k, want := range map[string]string{"MCP_API_TOKEN": "tok", "SECRET_X": "s", "VAR_Y": "v", "PATH": "/usr/bin"} {
		if have[k] != want {
			t.Errorf("%s = %q, want %q", k, have[k], want)
		}
	}
	if !strings.HasPrefix(have["PYTHONPATH"], "/run/tmp123") || !strings.HasSuffix(have["PYTHONPATH"], "/platform/helpers") {
		t.Errorf("PYTHONPATH = %q: the temp folder must come before the platform helpers", have["PYTHONPATH"])
	}
	if len(env) != 2 {
		t.Error("the caller's slice must not be modified in place")
	}
}
