//go:build linux

package slots

import "syscall"

// ApplyServiceUmask makes what a platform service creates closed to everyone but its owner and
// the owning slot's group. Call it once at startup; it does nothing unless slots are on.
func ApplyServiceUmask() {
	if Enabled() {
		syscall.Umask(0o007)
	}
}
