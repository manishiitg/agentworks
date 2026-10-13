package main

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/pkg/vaultruntime"
	"os"
	"strings"
)

func run() error {
	return runVaultRuntime(os.Getenv("AGENTWORKS_VAULT_BACKEND"), privateVaultRuntime())
}
func runVaultRuntime(mode string, private *vaultruntime.Factory) error {
	switch strings.TrimSpace(mode) {
	case "", "builtin":
		return runBuiltin()
	case "private":
		if private == nil || private.Version != vaultruntime.Version || private.Run == nil {
			return fmt.Errorf("private Vault runtime is unavailable or incompatible; use a composed gateway")
		}
		products, err := serverProductFactories()
		if err != nil {
			return err
		}
		return private.Run(products)
	default:
		return fmt.Errorf("unknown Vault backend %q", mode)
	}
}
