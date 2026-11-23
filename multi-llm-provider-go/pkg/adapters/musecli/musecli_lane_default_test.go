package musecli

import "os"

// The existing lane tests certify the tmux lane by reading its panes; the msp
// tests opt in with MUSE_CLI_TRANSPORT=msp. Muse's own sandbox stays on in
// every laptop live test.
func init() {
	if os.Getenv(MuseTransportEnv) == "" {
		_ = os.Setenv(MuseTransportEnv, "tmux")
	}
	museMSPKeepMuseSandbox = true
}
