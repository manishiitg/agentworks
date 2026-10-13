//go:build !windows

package main

import "errors"

// The Windows process primitives; other systems use ps.
func winProcList() ([]procEntry, error)  { return nil, errors.New("not Windows") }
func winProcRead(int) (procEntry, error) { return procEntry{}, errors.New("not Windows") }
func winProcCreated(uint32) uint64       { return 0 }
