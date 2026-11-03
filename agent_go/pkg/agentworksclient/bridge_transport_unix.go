//go:build !windows

package agentworksclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// bridgeDial connects to the local helper's private socket in root.
func bridgeDial(ctx context.Context, root string) (net.Conn, error) {
	socket := filepath.Join(root, "bridge.sock")
	info, err := os.Lstat(socket)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("unsafe local bridge socket: expected a socket")
	}
	if err = CheckPrivateAccess(socket, info); err != nil {
		return nil, fmt.Errorf("unsafe local bridge socket: %w", err)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", socket)
}

// bridgeListen opens the helper's private socket. The caller holds the listener lock,
// so an existing socket file is stale.
func bridgeListen(root string) (net.Listener, error) {
	socket := filepath.Join(root, "bridge.sock")
	if len(socket) >= 104 { // sun_path is 104 bytes on macOS, 108 on Linux
		return nil, errors.New("the AgentWorks bridge folder path is too long for a local socket (under 104 characters); shorten it")
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("refusing redirected bridge socket")
		}
		if err = os.Remove(socket); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
