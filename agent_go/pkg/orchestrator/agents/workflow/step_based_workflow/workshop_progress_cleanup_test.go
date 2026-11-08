package step_based_workflow

import (
	"strings"
)

type canceledRetryNotifier struct{ finished chan struct{} }

func (n *canceledRetryNotifier) OnExecutionStart(start WorkshopExecutionStart) {
	if start.Cancel != nil {
		// Admit a fresh execution through the real tool, then stop it before
		// any model or external action. The bug was refusal before admission.
		start.Cancel()
	}
}
func (n *canceledRetryNotifier) OnExecutionComplete(id, _, _ string, _ map[string]string, _ error) {
	if strings.HasPrefix(id, "exec-") {
		close(n.finished)
	}
}
func (*canceledRetryNotifier) OnExecutionTerminated(_, _ string) {}
