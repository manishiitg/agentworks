//go:build !darwin

package agentworksclient

import "os"

func syncJournalFile(f *os.File) error { return f.Sync() }
