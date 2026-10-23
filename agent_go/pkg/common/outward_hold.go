package common

import "context"

type outwardHeldKey struct{}

// WithOutwardHeld marks a tool call as made in a turn whose Outward permission
// is ask (Pulse Goal Work, pulse.autonomy.outward). Tools whose outward effect
// depends on their arguments (Slack writes, Google writes, notifications to
// other people) refuse that part. Only the server's tool binding sets it.
func WithOutwardHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, outwardHeldKey{}, true)
}

// OutwardHeld reports whether WithOutwardHeld marked ctx.
func OutwardHeld(ctx context.Context) bool {
	held, _ := ctx.Value(outwardHeldKey{}).(bool)
	return held
}

type goalWorkHeldKey struct{}

// WithGoalWorkHeld marks a tool call made in an Autopilot Goal Work turn. Tools
// that change Autopilot's own settings (autonomy level, on/off) refuse it: those
// stay with the owner. Only the server's tool binding sets it.
func WithGoalWorkHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, goalWorkHeldKey{}, true)
}

// GoalWorkHeld reports whether WithGoalWorkHeld marked ctx.
func GoalWorkHeld(ctx context.Context) bool {
	held, _ := ctx.Value(goalWorkHeldKey{}).(bool)
	return held
}
