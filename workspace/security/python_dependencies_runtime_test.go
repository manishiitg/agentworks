package security

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// PLAT-746-479: real installs must be script-local, shared by concurrent runs,
// reusable without a second install and normally unwritable by the program.
func TestPythonRequirementsInstallOnceAndRemainScriptLocal(t *testing.T) {
	if runtime.GOOS == "linux" {
		if _, ok := CLILandlockRunner(); !ok {
			t.Skip("needs the real Linux Landlock harness")
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	assets := t.TempDir()
	if output, err := exec.Command(python, "../../scripts/build-playwright-packages.py", assets).CombinedOutput(); err != nil {
		t.Fatalf("build runtime packages: %v\n%s", err, output)
	}
	t.Setenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR", assets)
	t.Setenv("NATIVE_WORKSPACE", "true")
	var wheel bytes.Buffer
	archive := zip.NewWriter(&wheel)
	for name, content := range map[string]string{
		"tiny_fixture/__init__.py":            "marker = 'fixture-package'\n",
		"tiny_fixture-1.0.dist-info/METADATA": "Metadata-Version: 2.1\nName: tiny-fixture\nVersion: 1.0\n",
		"tiny_fixture-1.0.dist-info/WHEEL":    "Wheel-Version: 1.0\nGenerator: fixture\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		"tiny_fixture-1.0.dist-info/RECORD":   "",
	} {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tiny_fixture-1.0-py3-none-any.whl" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			downloads.Add(1)
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(wheel.Bytes())
	}))
	defer server.Close()
	root := t.TempDir()
	code, output, cache := filepath.Join(root, "code"), filepath.Join(root, "run"), filepath.Join(root, ".sandbox-cache", "python-packages", "fixture")
	// Match the workspace handler: only the shared cache grant exists before
	// launch; the executing actor creates its own script and hash entries.
	for _, directory := range []string{code, output, filepath.Join(root, ".sandbox-cache")} {
		if err := os.MkdirAll(directory, 0770|os.ModeSetgid); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0770|os.ModeSetgid); err != nil {
			t.Fatal(err)
		}
	}
	iso := &Isolator{BaseDir: root, WorkDir: output, ReadPaths: []string{code, filepath.Join(root, ".sandbox-cache")}, WritePaths: []string{output, filepath.Join(root, ".sandbox-cache")}}
	if slot := os.Getenv("AGENTWORKS_BROWSER_HELPER_SLOT_E2E"); slot != "" {
		iso.Slot = slot
		for _, directory := range []string{root, filepath.Dir(root)} {
			if err := os.Chmod(directory, 0770|os.ModeSetgid); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Read-only package directories are intentional. Remove test artifacts as
	// the same actor that created them, restoring directory write bits only
	// for cleanup (including the deliberately trusted same-UID chmod case).
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		program := "import os; from pathlib import Path; root=Path(" + fmt.Sprintf("%q", filepath.Join(root, ".sandbox-cache")) + "); [p.chmod(0o2770) for p in [root, *root.rglob('*')] if p.is_dir() and p.stat().st_uid == os.geteuid()]"
		quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'" }
		cmd, cleanup, err := iso.ExecuteIsolated(ctx, python, []string{"-B", "-c", quote(program)})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Error(err)
			return
		}
		if result, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("package fixture cleanup: %v\n%s", err, result)
		}
	})
	program := `import os
from pathlib import Path
import tiny_fixture
assert tiny_fixture.marker == 'fixture-package'
assert Path(tiny_fixture.__file__).is_relative_to(Path(os.environ['WORKFLOW_CODE_DEPS']))
try:
    Path(tiny_fixture.__file__).write_text('changed')
except PermissionError:
    pass
else:
    raise AssertionError('a normal script write replaced installed packages')
print('package-import-and-write-refusal-ok')
`
	script := filepath.Join(code, "main.py")
	if err := os.WriteFile(script, []byte(program), 0640); err != nil {
		t.Fatal(err)
	}
	setRequirements := func(extra string) string {
		t.Helper()
		text := "tiny-fixture @ " + server.URL + "/tiny_fixture-1.0-py3-none-any.whl\n" + extra
		if err := os.WriteFile(filepath.Join(code, "requirements.txt"), []byte(text), 0640); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(cache, fmt.Sprintf("%x", sha256.Sum256([]byte(text))))
		iso.ExtraEnv = map[string]string{"WORKFLOW_RUN_DIR": output, "SCRIPT_SOURCE_DIR": code, "WORKFLOW_CODE_DEPS": target, "PYTHONPATH": code + string(os.PathListSeparator) + target, "SCRIPT_INSTALL_REQUIREMENTS": "1"}
		return target
	}
	firstTarget := setRequirements("")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'" }
	command := quote(python) + " -B -m agentworks._dependencies && " + quote(python) + " -B " + quote(script)
	run := func() (*exec.Cmd, *bytes.Buffer) {
		t.Helper()
		cmd, cleanup, err := iso.ExecuteIsolated(ctx, "/bin/sh", []string{"-c", quote(command)})
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
		if err != nil {
			t.Fatal(err)
		}
		// The real workspace handler merges request env after isolation for
		// app commands; slot commands already serialize ExtraEnv in the wrapper.
		if iso.Slot == "" {
			cmd.Env = MergeExtraEnv(cmd.Env, iso.ExtraEnv)
		}
		log := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, log
	}
	first, firstLog := run()
	second, secondLog := run()
	for _, result := range []struct {
		command *exec.Cmd
		log     *bytes.Buffer
	}{{first, firstLog}, {second, secondLog}} {
		if err := result.command.Wait(); err != nil || !strings.Contains(result.log.String(), "package-import-and-write-refusal-ok") {
			t.Fatalf("real package run: %v\n%s", err, result.log)
		}
	}
	logs := firstLog.String() + secondLog.String()
	if downloads.Load() < 1 || strings.Count(logs, "[packages] Installing") != 1 || strings.Count(logs, "[packages] Reusing") != 1 {
		t.Fatalf("concurrent runs did not share one complete installation: downloads=%d\n%s", downloads.Load(), logs)
	}
	if next := setRequirements("# changed requirements\n"); next == firstTarget {
		t.Fatal("changed requirement bytes reused the old entry")
	}
	third, thirdLog := run()
	if err := third.Wait(); err != nil || !strings.Contains(thirdLog.String(), "[packages] Installing") || !strings.Contains(thirdLog.String(), "package-import-and-write-refusal-ok") {
		t.Fatalf("changed requirements did not produce a usable new entry: %v\n%s", err, thirdLog)
	}
	if _, err := os.Stat(filepath.Join(firstTarget, "tiny_fixture", "__init__.py")); err != nil {
		t.Fatalf("a new entry removed a prior run's packages: %v", err)
	}
	uv := filepath.Join(assets, "uv")
	if runtime.GOOS == "windows" {
		uv += ".exe"
	}
	if err := os.Rename(uv, uv+".held"); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(python, "-c", "import pip").Run(); err == nil {
		setRequirements("# pip fallback\n")
		fallback, fallbackLog := run()
		if err := fallback.Wait(); err != nil || !strings.Contains(fallbackLog.String(), "with pip") || !strings.Contains(fallbackLog.String(), "package-import-and-write-refusal-ok") {
			t.Fatalf("pip fallback failed: %v\n%s", err, fallbackLog)
		}
	} else {
		t.Log("pip fallback not exercised: this interpreter has no pip; uv path was verified")
	}
	// -S removes host pip while retaining our staged standard-library helper.
	// A fresh entry must explain the setup failure, never install globally.
	setRequirements("# no installer\n")
	command = quote(python) + " -S -B -m agentworks._dependencies"
	missing, missingLog := run()
	if err := missing.Wait(); err == nil || !strings.Contains(missingLog.String(), "uv and pip are missing") {
		t.Fatalf("missing installer did not give a plain setup error: %v\n%s", err, missingLog)
	}
	// PR1594: an included pin could change while the top-level hash stayed
	// unchanged. Refuse indirection even if an older cached entry exists.
	for _, option := range []string{"-r pins.txt", "--constraint=pins.txt", "--find-links=packages"} {
		setRequirements(option + "\n")
		command = quote(python) + " -B -c " + quote("import os; from pathlib import Path; Path(os.environ['WORKFLOW_CODE_DEPS']).mkdir(parents=True, exist_ok=True)")
		cached, cachedLog := run()
		if err := cached.Wait(); err != nil {
			t.Fatalf("prepare existing cache entry: %v\n%s", err, cachedLog)
		}
		command = quote(python) + " -B -m agentworks._dependencies"
		refused, refusedLog := run()
		if err := refused.Wait(); err == nil || !strings.Contains(refusedLog.String(), "Put the package pins directly in requirements.txt") || strings.Contains(refusedLog.String(), "[packages] Reusing") {
			t.Fatalf("included package inputs silently reused a stale entry: %v\n%s", err, refusedLog)
		}
	}
}
