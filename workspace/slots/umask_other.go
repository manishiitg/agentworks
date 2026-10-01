//go:build !linux

package slots

// ApplyServiceUmask does nothing: slots are Linux accounts.
func ApplyServiceUmask() {}
