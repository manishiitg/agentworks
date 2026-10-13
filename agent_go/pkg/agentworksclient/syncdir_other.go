//go:build !windows

package agentworksclient

import "os"

// syncJournalDir flushes a directory so a rename inside it survives a crash.
func syncJournalDir(dir *os.File) error { return syncJournalFile(dir) }
