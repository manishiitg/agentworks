//go:build windows

package agentworksclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// The Windows helper listens on a named pipe, not an AF_UNIX socket file: on some
// Windows installs socket files cannot be opened or connected to at all
// ("connect: An invalid argument was supplied", PLAT-874). One pipe per bridge folder.
func bridgePipeName(root string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root))))
	return `\\.\pipe\agentworks-bridge-` + hex.EncodeToString(sum[:12])
}

// bridgeDial connects to the helper's pipe and checks the pipe server runs as this
// Windows user, so a pipe created first by another user is never trusted.
func bridgeDial(ctx context.Context, root string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, bridgePipeName(root))
	if err != nil {
		return nil, err
	}
	if err = verifyBridgePipeServer(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func verifyBridgePipeServer(conn net.Conn) error {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("cannot inspect the AgentWorks bridge pipe")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid); err != nil {
		return fmt.Errorf("cannot verify the AgentWorks bridge pipe: %w", err)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("cannot verify the AgentWorks bridge pipe: %w", err)
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("cannot verify the AgentWorks bridge pipe: %w", err)
	}
	defer token.Close()
	owner, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !owner.User.Sid.Equals(current.User.Sid) {
		return errors.New("the AgentWorks bridge pipe belongs to another Windows user")
	}
	return nil
}

// bridgeListen creates the helper's pipe, open only to this Windows user. The caller
// holds the listener lock; winio creates the first instance, so it fails if another
// process already owns the name.
func bridgeListen(root string) (net.Listener, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sddl := "D:P(A;;GA;;;" + user.User.Sid.String() + ")"
	return winio.ListenPipe(bridgePipeName(root), &winio.PipeConfig{SecurityDescriptor: sddl, InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
}
