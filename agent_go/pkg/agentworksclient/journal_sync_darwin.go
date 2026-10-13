//go:build darwin

package agentworksclient

import (
	"os"
	"syscall"
)

// syncJournalFile flushes the file to the drive. On macOS os.File.Sync asks the drive to empty
// its own cache too (F_FULLFSYNC), which costs milliseconds per save and the bridge saves on
// every message and acknowledgement while holding its lock. A plain fsync survives a crash of
// this process, which is what the journal guards against.
func syncJournalFile(f *os.File) error { return syscall.Fsync(int(f.Fd())) }
