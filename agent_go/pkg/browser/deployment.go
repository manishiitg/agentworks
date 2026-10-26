package browser

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// VisibleModes is the installation's browser choice contract. Personal browser
// identities remain in account connections; selecting a mode never pairs one.
func VisibleModes() []string {
	if productpolicy.Local() {
		return []string{"extension", "cdp"}
	}
	return []string{"extension", "headless"}
}

func ConfigurationModes() []string { return append(VisibleModes(), "none") }

func ValidateMode(mode string) error {
	for _, allowed := range ConfigurationModes() {
		if mode == allowed {
			return nil
		}
	}
	return fmt.Errorf("Choose a browser mode from %s", strings.Join(ConfigurationModes(), ", "))
}

func ManagedEnabled() bool { return !productpolicy.Local() }
