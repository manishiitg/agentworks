package step_based_workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

type blockingWorkshopExecutionNotifier struct {
	started chan WorkshopExecutionStart
	release chan struct{}
}

func (n *blockingWorkshopExecutionNotifier) OnExecutionStart(start WorkshopExecutionStart) {
	n.started <- start
	<-n.release
}

func (*blockingWorkshopExecutionNotifier) OnExecutionComplete(string, string, string, map[string]string, error) {
}

func (*blockingWorkshopExecutionNotifier) OnExecutionTerminated(string, string) {}

func TestRegisterWorkshopExecutionBeforeLaunchIsSynchronous(t *testing.T) {
	notifier := &blockingWorkshopExecutionNotifier{
		started: make(chan WorkshopExecutionStart, 1),
		release: make(chan struct{}),
	}
	returned := make(chan struct{})
	go func() {
		registerWorkshopExecutionBeforeLaunch(notifier, WorkshopExecutionStart{ID: "exec-step-1"})
		close(returned)
	}()

	select {
	case start := <-notifier.started:
		if start.ID != "exec-step-1" {
			t.Fatalf("registered execution = %q, want exec-step-1", start.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("execution was not registered")
	}

	select {
	case <-returned:
		t.Fatal("launch boundary returned before execution registration completed")
	default:
	}

	close(notifier.release)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("launch boundary did not return after registration completed")
	}
}

// isExecutionRegistrationCall reports whether an expression registers a workshop
// execution with the server-side registry, under either the helper or a direct
// notifier call.
func isExecutionRegistrationCall(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "registerWorkshopExecutionBeforeLaunch"
	case *ast.SelectorExpr:
		return fn.Sel.Name == "OnExecutionStart"
	}
	return false
}

// TestWorkshopExecutionsRegisterBeforeTheirGoroutineLaunches pins the call-site
// ordering, which is where the bug actually lived: registration used to happen
// inside the launched goroutine, so a scheduled parent turn could go idle before
// the child entered the registry. The scheduler then advanced and stamped a
// truncated run "success" — observed on latency-report's 2026-07-31 dev cron run,
// recorded as success/84.6s while the pipeline stopped at step 5 of 9.
//
// TestRegisterWorkshopExecutionBeforeLaunchIsSynchronous only covers the helper,
// so moving the call back inside a `go func()` would still pass it. This asserts
// the property that actually matters: no execution registration is reachable
// from inside a goroutine body.
func TestWorkshopExecutionsRegisterBeforeTheirGoroutineLaunches(t *testing.T) {
	const path = "interactive_workshop_manager.go"
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	deferredRegistrations := 0
	ast.Inspect(file, func(node ast.Node) bool {
		goStmt, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		ast.Inspect(goStmt, func(inner ast.Node) bool {
			if isExecutionRegistrationCall(inner) {
				deferredRegistrations++
			}
			return true
		})
		return true
	})
	if deferredRegistrations > 0 {
		t.Fatalf("%d execution registration(s) happen inside a goroutine in %s; "+
			"register before launching so the scheduler cannot observe the parent as idle "+
			"while the child is still starting", deferredRegistrations, path)
	}

	// Guard against passing vacuously if registration is removed altogether.
	registrations := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if isExecutionRegistrationCall(node) {
			registrations++
		}
		return true
	})
	if registrations == 0 {
		t.Fatalf("no execution registration calls found in %s; the ordering invariant is no longer covered", path)
	}
}
