package agycli

import "os"

// The older lane tests certify the tmux lane; the ACP tests opt in with
// AGY_CLI_TRANSPORT=acp.
func init() {
	if os.Getenv(AgyTransportEnv) == "" {
		_ = os.Setenv(AgyTransportEnv, "tmux")
	}
}
