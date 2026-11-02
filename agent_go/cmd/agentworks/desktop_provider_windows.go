package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func windowsCodexPackageRoots() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`Get-AppxPackage | Where-Object { $_.Name -match '^OpenAI\.(Codex|ChatGPT)' } | ForEach-Object { $_.InstallLocation }`).Output()
	if err != nil {
		return nil
	}
	var roots []string
	for _, line := range strings.Split(string(out), "\n") {
		if root := strings.TrimSpace(line); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}
