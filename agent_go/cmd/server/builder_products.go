package server

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"

// The installation is the upper bound; account product access narrows builder
// capabilities. Tool-specific group, folder, role and regex checks still run.
func builderProductSelection(claims *UserClaims) productpolicy.Selection {
	return productpolicy.Selection{Allowed: func(product string) bool {
		return userAllowedProduct(claims, product)
	}}
}
