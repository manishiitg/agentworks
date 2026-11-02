//go:build windows

package server

// syncDirectory: Windows cannot open a directory to flush it ("Access is
// denied"), and NTFS journals directory entries itself.
func syncDirectory(string) error { return nil }
