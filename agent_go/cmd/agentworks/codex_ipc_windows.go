package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const codexWindowsPipe = `\\.\pipe\codex-ipc`

type codexWindowsConn struct {
	net.Conn
	serverPID uint32
}

var codexWindowsRoots struct {
	sync.Mutex
	paths []string
}

func dialCodexDesktop(ctx context.Context, remote string) (net.Conn, error) {
	if remote != "desktop+npipe://codex-ipc" {
		return nil, errors.New("native Windows delivery requires the Codex desktop pipe")
	}
	// Identification lets Codex identify the caller, without impersonating it.
	conn, err := winio.DialPipeAccessImpLevel(ctx, codexWindowsPipe, windows.GENERIC_READ|windows.GENERIC_WRITE, winio.PipeImpLevelIdentification)
	if err != nil {
		return nil, fmt.Errorf("could not connect to the Codex desktop pipe: %w", err)
	}
	pid, err := verifyCodexWindowsPipe(conn, codexWindowsImageInstalled)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &codexWindowsConn{Conn: conn, serverPID: pid}, nil
}

// Named pipes can be squatted. Authenticate the exact connected handle before
// initialize, thread metadata or message contents are written to it.
func verifyCodexWindowsPipe(conn net.Conn, imageAllowed func(string) bool) (uint32, error) {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("cannot inspect the Codex desktop pipe")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid); err != nil {
		return 0, fmt.Errorf("cannot verify the Codex pipe server: %w", err)
	}
	image, err := codexWindowsProcessImage(pid)
	if err != nil {
		return 0, err
	}
	if !imageAllowed(image) {
		return 0, errors.New("Codex pipe server is outside the installed Codex app")
	}
	return pid, nil
}

func codexWindowsProcessImage(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("cannot inspect the Codex desktop process: %w", err)
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", fmt.Errorf("cannot inspect the Codex desktop user: %w", err)
	}
	defer token.Close()
	owner, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	if !owner.User.Sid.Equals(current.User.Sid) {
		return "", errors.New("Codex desktop belongs to another Windows user")
	}
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if err = windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", fmt.Errorf("cannot read the Codex desktop image: %w", err)
	}
	return windows.UTF16ToString(buf[:n]), nil
}

func codexWindowsImageUnder(image string, roots []string) bool {
	image = strings.ToLower(filepath.Clean(strings.TrimPrefix(image, `\\?\`)))
	for _, root := range roots {
		root = strings.ToLower(filepath.Clean(strings.TrimPrefix(root, `\\?\`)))
		if filepath.IsAbs(root) && strings.HasPrefix(image, root+`\`) {
			return true
		}
	}
	return false
}

func codexWindowsImageInstalled(image string) bool {
	codexWindowsRoots.Lock()
	defer codexWindowsRoots.Unlock()
	if codexWindowsImageUnder(image, codexWindowsRoots.paths) {
		return true
	}
	// Cache successful installation metadata, refreshing when an app update
	// moves the image. Never widen pipe ACLs or trust a root supplied by a peer.
	codexWindowsRoots.paths = windowsCodexPackageRoots()
	return codexWindowsImageUnder(image, codexWindowsRoots.paths)
}

func windowsCodexDesktopHost(ctx context.Context, remote string) (string, error) {
	conn, err := dialCodexDesktop(ctx, remote)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	peer := conn.(*codexWindowsConn).serverPID
	list, err := winProcList()
	if err != nil {
		return "", err
	}
	var host string
	for _, process := range list {
		if process.PPID != int(peer) || !strings.EqualFold(procBaseName(process.Name), "codex") {
			continue
		}
		image, err := codexWindowsProcessImage(uint32(process.PID))
		if err != nil || !codexWindowsImageInstalled(image) {
			continue
		}
		// Electron's own renderer children use the app image. The core is the
		// distinct native executable in resources, as in the desktop package.
		lower := strings.ToLower(filepath.Clean(image))
		if !strings.HasSuffix(lower, `\resources\codex.exe`) && !strings.HasSuffix(lower, `\resources\codex-cli\bin\codex.exe`) {
			continue
		}
		process.Created = winProcCreated(uint32(process.PID))
		if process.Created == 0 {
			continue
		}
		if host != "" {
			return "", errors.New("Codex desktop has more than one local core")
		}
		host = winHostKey(process)
	}
	if host == "" {
		return "", errors.New("Codex desktop core is unavailable")
	}
	return host, nil
}
