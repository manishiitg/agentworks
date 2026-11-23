package llmtypes

import "fmt"

// ValidateCLISecurityLaunch prevents an adapter from silently ignoring a
// stricter policy. Provider adapters should call this before any process is
// launched. As providers gain real enforcement, their adapter can replace this
// baseline check with a provider-specific validator.
func ValidateCLISecurityLaunch(opts *CallOptions, enforcedModes ...CLISecurityMode) error {
	if opts == nil || opts.CLISecurity == nil {
		return nil
	}
	mode := NormalizeCLISecurityMode(opts.CLISecurity.Mode)
	if mode == CLISecurityModeCompatibility {
		return nil
	}
	for _, enforced := range enforcedModes {
		if mode == NormalizeCLISecurityMode(enforced) {
			return nil
		}
	}
	return fmt.Errorf("CLI security mode %q is not enforced by this provider adapter", mode)
}
