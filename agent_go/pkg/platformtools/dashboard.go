// Package platformtools projects admitted native tools onto the shared action
// catalog. CLI clients import its catalog subpackage without the server runtime.
package platformtools

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"

type Action = catalog.Action

var (
	DashboardActions     = catalog.DashboardActions
	ConfigurationActions = catalog.ConfigurationActions
	DataActions          = catalog.DataActions
	ProjectActions       = catalog.ProjectActions
	AgentActions         = catalog.AgentActions
	ExperienceActions    = catalog.ExperienceActions
	Actions              = catalog.Actions
)

func Identity(member string) string      { return catalog.Identity(member) }
func PublicName(identity string) string  { return catalog.PublicName(identity) }
func IsDashboardMember(name string) bool { return catalog.IsDashboardMember(name) }
func IsMember(name string) bool          { return catalog.IsMember(name) }
func IsFamily(name string) bool          { return catalog.IsFamily(name) }
func Invocation(source string, args map[string]any) (string, map[string]any) {
	return catalog.Invocation(source, args)
}
