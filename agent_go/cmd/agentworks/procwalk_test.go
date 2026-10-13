package main

import (
	"errors"
	"testing"
)

// A process id can be reused after its process ends. The walk up from a process must stop at a "parent" that
// started after its child, and must find a provider process further up when the chain is sound.
func TestWalkAncestorsStopsAtAReusedParentId(t *testing.T) {
	table := map[int]procEntry{
		10: {PID: 10, PPID: 9, Name: "agentworks.exe", Created: 500},
		9:  {PID: 9, PPID: 8, Name: "cmd.exe", Created: 400},
		8:  {PID: 8, PPID: 1, Name: "claude.exe", Created: 300},
	}
	read := func(pid int) (procEntry, error) {
		if e, ok := table[pid]; ok {
			return e, nil
		}
		return procEntry{}, errors.New("gone")
	}
	isClaude := func(p procEntry) bool { return procBaseName(p.Name) == "claude" }
	if e, err := walkAncestors(10, 8, read, isClaude); err != nil || e.PID != 8 {
		t.Fatalf("sound chain = %+v, %v", e, err)
	}
	table[8] = procEntry{PID: 8, PPID: 1, Name: "claude.exe", Created: 450} // began after cmd.exe: a reused id
	if e, err := walkAncestors(10, 8, read, isClaude); err == nil {
		t.Fatalf("followed a reused parent id to %+v", e)
	}
	table[8] = procEntry{PID: 8, PPID: 1, Name: "claude.exe"} // start time unreadable: do not follow it unchecked
	if e, err := walkAncestors(10, 8, read, isClaude); err == nil {
		t.Fatalf("followed a parent with no start time to %+v", e)
	}
	if procBaseName("Claude.EXE") != "claude" {
		t.Fatal("executable names must match without .exe and case")
	}
}
