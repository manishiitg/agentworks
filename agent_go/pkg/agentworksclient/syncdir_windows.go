package agentworksclient

import "os"

// syncJournalDir: Windows cannot flush a directory handle ("Access is denied"), and NTFS makes a
// completed rename durable on its own, so there is nothing to sync.
func syncJournalDir(*os.File) error { return nil }
