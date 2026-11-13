//go:build !linux && !darwin

package server

// SIGUSR1 is a local Unix diagnostic; other targets keep their signal behavior.
func startLocalProfileSignal() func() { return func() {} }
