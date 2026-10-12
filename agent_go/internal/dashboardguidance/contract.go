// Package dashboardguidance owns the HTML Dashboard contract shared by product
// skills and workflow references. Product wrappers own execution/access rules.
package dashboardguidance

import (
	_ "embed"
	"strings"
)

//go:embed common.md
var common string

// Content returns the canonical runtime/authoring contract.
func Content() string { return common }

// Expand composes a product wrapper with the shared contract before attaching
// it to an agent. The agent needs no filesystem access to another product.
// Workflow callers expand after Go template evaluation: the contract includes
// literal managed-revision placeholders such as {{dashboard_assets}}.
func Expand(wrapper string) string {
	return strings.ReplaceAll(wrapper, "<!-- dashboard:shared -->", common)
}
