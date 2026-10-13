package main

import (
	"errors"
	"strings"
)

// procEntry is one process as the platform reports it. Created is the start time in any unit that
// only grows (zero when unknown); Name is the executable's base name.
type procEntry struct {
	PID, PPID int
	Name      string
	Created   uint64
}

// procBaseName lowercases an executable name and drops a Windows ".exe", so "Claude.EXE" matches "claude".
func procBaseName(name string) string {
	name = strings.ToLower(name)
	return strings.TrimSuffix(name, ".exe")
}

var errNoProviderAncestor = errors.New("not running inside an exact provider session")

// walkAncestors visits the parents of start, nearest first, up to depth of them, and returns the first
// one visit accepts. A process id can be reused after its process ends, so a "parent" that began after its
// child cannot be the real parent and the walk stops there instead of following an unrelated process.
func walkAncestors(start, depth int, read func(int) (procEntry, error), visit func(procEntry) bool) (procEntry, error) {
	child, err := read(start)
	if err != nil {
		return procEntry{}, err
	}
	for i := 0; i < depth && child.PPID > 1; i++ {
		parent, err := read(child.PPID)
		if err != nil {
			return procEntry{}, errors.New("cannot identify provider ancestor")
		}
		if parent.Created != 0 && child.Created != 0 && parent.Created > child.Created {
			return procEntry{}, errors.New("a parent process id was reused; cannot identify the provider ancestor")
		}
		if visit(parent) {
			return parent, nil
		}
		child = parent
	}
	return procEntry{}, errNoProviderAncestor
}
