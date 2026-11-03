//go:build !windows

package main

import (
	"context"
	"errors"
	"net"
	"strings"
)

func dialCodexDesktop(ctx context.Context, remote string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", strings.TrimPrefix(strings.TrimPrefix(remote, "desktop+"), "unix://"))
}

func windowsCodexDesktopHost(context.Context, string) (string, error) {
	return "", errors.New("Windows desktop IPC is unavailable on this platform")
}

func windowsCodexDesktopHasCore(context.Context, string, string) error {
	return errors.New("Windows desktop IPC is unavailable on this platform")
}
