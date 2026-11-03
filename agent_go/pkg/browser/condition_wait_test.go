package browser

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// PLAT-746-308: a cancelled 180s readiness wait left daemon work behind,
// so the very next snapshot could not even list tabs.
func TestCancelledConditionWaitLeavesNextSnapshotAvailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newFakeWorkspace(t, func(command string, nth int) ShellExecuteResponse {
		if verbOf(command) == "eval" {
			cancel()
			return ShellExecuteResponse{Stdout: `{"success":true,"data":{"result":false}}`}
		}
		return ShellExecuteResponse{Stdout: `{"success":true,"data":{"snapshot":"ready"}}`}
	})
	client := NewClient(fake.server.URL)
	_, err := client.ExecuteCommand(ctx, []string{"--session", "isolated", "wait", "--fn", "document.readyState === 'complete'", "--timeout", "180000", "--cdp", "ws://localhost/private", "--pin-tab", "--json"}, &ExecuteOptions{Timeout: time.Minute})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait cancellation: %v", err)
	}
	output, err := client.ExecuteCommand(context.Background(), []string{"--session", "isolated", "snapshot", "--json"}, nil)
	if err != nil || !strings.Contains(output, "ready") {
		t.Fatalf("next snapshot: %s %v", output, err)
	}
	if fake.count("wait") != 0 || fake.count("eval") != 1 || fake.count("snapshot") != 1 {
		t.Fatalf("daemon received a lingering wait or extra poll: %v", fake.commands)
	}
	first := fake.commands[0]
	for _, flag := range []string{"isolated", "--cdp", "ws://localhost/private", "--pin-tab"} {
		if !strings.Contains(first, flag) {
			t.Errorf("lost existing transport scope %s", flag)
		}
	}
}

func TestConditionWaitHonorsCallerDeadlineAndReadyResult(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller deadline", true: "ready"}[ready], func(t *testing.T) {
			fake := newFakeWorkspace(t, func(_ string, _ int) ShellExecuteResponse {
				result := "false"
				if ready {
					result = "true"
				}
				return ShellExecuteResponse{Stdout: `{"success":true,"data":{"result":` + result + `}}`}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			output, err := NewClient(fake.server.URL).ExecuteCommand(ctx, []string{"--session", "isolated", "wait", "--fn", "window.ready", "--timeout", "180000", "--json"}, &ExecuteOptions{Timeout: time.Minute})
			if ready {
				if err != nil || !strings.Contains(output, `"success":true`) {
					t.Fatalf("ready: %s %v", output, err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline: %v", err)
			}
			if fake.count("wait") != 0 || fake.count("eval") != 1 {
				t.Fatalf("unexpected daemon work: %v", fake.commands)
			}
		})
	}
}

func TestConditionWaitRecoversFromNavigationEvaluationError(t *testing.T) {
	fake := newFakeWorkspace(t, func(_ string, nth int) ShellExecuteResponse {
		if nth == 1 {
			return ShellExecuteResponse{ExitCode: 1, Stderr: "Execution context was destroyed"}
		}
		return ShellExecuteResponse{Stdout: `{"success":true,"data":{"result":true}}`}
	})
	output, err := NewClient(fake.server.URL).ExecuteCommand(context.Background(), []string{"--session", "isolated", "wait", "--fn", "window.ready", "--json"}, &ExecuteOptions{Timeout: 2 * time.Second})
	if err != nil || !strings.Contains(output, `"success":true`) || fake.count("eval") != 2 {
		t.Fatalf("navigation recovery: %s %v, calls=%v", output, err, fake.commands)
	}
}

// Evaluate the actual wrapper, so function/Promise truthiness cannot make a
// not-ready page pass immediately. Node is a browser-runtime prerequisite.
func TestConditionWaitEvaluatesFunctionAndAsyncResults(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	for _, condition := range []string{"false", "() => false", "async () => false", "Promise.resolve(false)", "new Promise(() => {})", "() => true", "async () => true"} {
		t.Run(condition, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, node, "-e", conditionWaitExpression(condition)+".then(v => process.stdout.write(String(v)))").CombinedOutput()
			want := "false"
			if strings.Contains(condition, "true") {
				want = "true"
			}
			if err != nil || string(output) != want {
				t.Fatalf("condition = %s, want %s: %v", output, want, err)
			}
		})
	}
}
