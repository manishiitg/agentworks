//go:build !linux

package main

func localShellLauncher([]string) (bool, int) { return false, 0 }
func configureLocalShellSandbox() error       { return nil }
