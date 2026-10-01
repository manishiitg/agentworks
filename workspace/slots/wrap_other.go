//go:build !linux

package slots

import (
	"context"
	"errors"
	"os/exec"
)

// WrapCommand is Linux-only: slots are Linux accounts.
func WrapCommand(_ context.Context, _ *exec.Cmd, _ string) (*exec.Cmd, error) {
	return nil, errors.New("slots are only supported on Linux")
}
