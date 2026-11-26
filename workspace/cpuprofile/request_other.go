//go:build !linux

package cpuprofile

// Start is a no-op outside the hosted Linux services.
func Start(service string) {}
