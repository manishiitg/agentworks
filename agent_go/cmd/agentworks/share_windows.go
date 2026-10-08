//go:build windows

package main

import "os/exec"

func detach(*exec.Cmd)           {}
func processAlive(int) bool      { return false }
func terminateProcess(int, bool) {}
func stdinIsTerminal() bool      { return false }
