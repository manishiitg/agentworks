package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsInstallerChecksFinalExecutable(t *testing.T) {
	script, _ := filepath.Abs("../../../deploy/common/test_windows_cli_installer.ps1")
	installer, _ := filepath.Abs("../../../scripts/install-agentworks-cli.ps1")
	// The native Windows verification job builds this CLI before its named tests.
	executable, _ := filepath.Abs("../../agentworks.exe")
	out, err := exec.Command("powershell.exe", "-NoProfile", "-File", script, "-Installer", installer, "-Executable", executable).CombinedOutput()
	if err != nil {
		t.Fatalf("real PowerShell install failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}
