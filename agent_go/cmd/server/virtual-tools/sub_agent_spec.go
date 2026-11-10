package virtualtools

import "context"

// SubAgentSpec is the typed execution link carried by a context: which
// registered background execution (workflow run, step, agent item or turn)
// the work in this context belongs to.
//
// Note: call_sub_agent and call_scripted_sub_agent (the workflow-orchestrator
// route paths) have their own separate keys in sub_agent_tools.go and are not
// covered by this spec.
type SubAgentSpec struct {
	// BackgroundAgentID links child work to its registry entry so its events
	// and nested spawns can reference the parent execution.
	BackgroundAgentID string
}

type subAgentSpecKeyType struct{}

var subAgentSpecKey subAgentSpecKeyType

// DefaultSubAgentSpec returns the spec for a context with no execution link.
func DefaultSubAgentSpec() SubAgentSpec {
	return SubAgentSpec{}
}

// WithSubAgentSpec returns a context carrying the spec.
func WithSubAgentSpec(ctx context.Context, spec SubAgentSpec) context.Context {
	return context.WithValue(ctx, subAgentSpecKey, spec)
}

// SubAgentSpecFromContext returns the spec carried by ctx, or
// DefaultSubAgentSpec when none is set.
func SubAgentSpecFromContext(ctx context.Context) SubAgentSpec {
	if spec, ok := ctx.Value(subAgentSpecKey).(SubAgentSpec); ok {
		return spec
	}
	return DefaultSubAgentSpec()
}

// WithBackgroundAgentID returns a context whose SubAgentSpec carries the given
// background agent ID. Used by background/step spawners that only need to
// link child events to a parent execution.
func WithBackgroundAgentID(ctx context.Context, agentID string) context.Context {
	spec := SubAgentSpecFromContext(ctx)
	spec.BackgroundAgentID = agentID
	return WithSubAgentSpec(ctx, spec)
}
