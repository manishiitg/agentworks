package server

import (
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

var errFlowsDisabled = errors.New(productpolicy.FlowsDisabledMessage)

func localProductInstallation() bool        { return productpolicy.Local() }
func localServerProductsEnabled() bool      { return productpolicy.LocalServerProducts() }
func serverOnlyProduct(product string) bool { return productpolicy.ServerOnly(product) }
func installationProductAvailable(product string) bool {
	return productpolicy.InstallationAvailable(product)
}
