//go:build !darwin

package security

import (
	"context"
	"errors"
	"os/exec"
)

// ExecuteLightLocal exists on macOS only; Linux keeps the Landlock sandbox and Windows has none.
func ExecuteLightLocal(context.Context, string, LightLocalPolicy, string) (*exec.Cmd, func(), error) {
	return nil, nil, errors.New("the light local sandbox is for macOS")
}
