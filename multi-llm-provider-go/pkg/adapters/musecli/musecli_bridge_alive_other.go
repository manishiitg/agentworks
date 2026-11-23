//go:build !linux

package musecli

func museReadProcessTable() (map[int]museProc, bool) { return nil, false }
