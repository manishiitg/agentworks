package server

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"

func localProductInstallation() bool        { return productpolicy.Local() }
func localServerProductsEnabled() bool      { return productpolicy.LocalServerProducts() }
func serverOnlyProduct(product string) bool { return productpolicy.ServerOnly(product) }
func installationProductAvailable(product string) bool {
	return productpolicy.InstallationAvailable(product)
}
