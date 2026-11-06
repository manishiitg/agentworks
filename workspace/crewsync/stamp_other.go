//go:build !linux && !darwin

package crewsync

import "io/fs"

// FileInfo on these systems does not expose a trusted change-time/identity.
// Leave it unknown so a cache rechecks content rather than missing an edit.
func stampIdentity(fs.FileInfo) (int64, uint64, uint64) { return 0, 0, 0 }
