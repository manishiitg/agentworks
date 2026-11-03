package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const codexWindowsPipe = `\\.\pipe\codex-ipc`

type codexWindowsConn struct {
	net.Conn
	serverPID uint32
}

// codexWindowsPackageFamily is the Codex desktop's Appx package family (name +
// publisher ID, owner's Windows install 2 Nov 2026). Windows assigns a process its
// package identity, so a same-user program outside this package cannot pass.
const codexWindowsPackageFamily = "OpenAI.Codex_2p2nqsd0c76g0"

func codexWindowsPackageAllowed(family string) bool {
	return strings.EqualFold(family, codexWindowsPackageFamily)
}

// codexWindowsPackageRoot returns the install folder of the package the process
// runs in; tests substitute an unpackaged fixture folder.
var codexWindowsPackageRoot = windowsProcessPackageRoot

var (
	kernel32Package               = windows.NewLazySystemDLL("kernel32.dll")
	procGetPackageFullName        = kernel32Package.NewProc("GetPackageFullName")
	procGetPackagePathByFullName  = kernel32Package.NewProc("GetPackagePathByFullName")
	procPackageFamilyNameFromFull = kernel32Package.NewProc("PackageFamilyNameFromFullName")
)

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
func verifyCodexWindowsPipe(conn net.Conn, imageAllowed func(uint32) bool) (uint32, error) {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("cannot inspect the Codex desktop pipe")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid); err != nil {
		return 0, fmt.Errorf("cannot verify the Codex pipe server: %w", err)
	}
	if !imageAllowed(pid) {
		image, _ := codexWindowsProcessImage(pid)
		_, why := codexWindowsPackageRoot(pid)
		return 0, fmt.Errorf("Codex pipe server is outside the installed Codex app (process %d, image %q, package: %v)", pid, image, why)
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

// codexWindowsImageInstalled: same Windows user, and the process image lies
// inside the install folder of an allowed Codex package.
func codexWindowsImageInstalled(pid uint32) bool {
	image, err := codexWindowsProcessImage(pid)
	if err != nil {
		return false
	}
	root, err := codexWindowsPackageRoot(pid)
	return err == nil && codexWindowsImageUnder(image, []string{root})
}

func windowsProcessPackageRoot(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	full := make([]uint16, 256)
	n := uint32(len(full))
	if r, _, _ := procGetPackageFullName.Call(uintptr(h), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&full[0]))); r != 0 {
		return "", fmt.Errorf("Codex pipe server has no app package (code %d)", r)
	}
	family := make([]uint16, 256)
	m := uint32(len(family))
	if r, _, _ := procPackageFamilyNameFromFull.Call(uintptr(unsafe.Pointer(&full[0])), uintptr(unsafe.Pointer(&m)), uintptr(unsafe.Pointer(&family[0]))); r != 0 {
		return "", fmt.Errorf("cannot read the Codex package family (code %d)", r)
	}
	if !codexWindowsPackageAllowed(windows.UTF16ToString(family)) {
		return "", fmt.Errorf("pipe server package %s is not the Codex app", windows.UTF16ToString(family))
	}
	path := make([]uint16, windows.MAX_LONG_PATH)
	k := uint32(len(path))
	if r, _, _ := procGetPackagePathByFullName.Call(uintptr(unsafe.Pointer(&full[0])), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&path[0]))); r != 0 {
		return "", fmt.Errorf("cannot read the Codex install folder (code %d)", r)
	}
	return windows.UTF16ToString(path), nil
}

func windowsCodexDesktopHost(ctx context.Context, remote string) (string, error) {
	cores, err := windowsCodexDesktopCores(ctx, remote)
	switch {
	case err != nil:
		return "", err
	case len(cores) > 1:
		return "", errors.New("Codex desktop has more than one local core")
	}
	return cores[0], nil
}

// windowsCodexDesktopHasCore reports whether host is one of the verified pipe
// server's own codex.exe cores. The Codex app can run several (PLAT-874: the
// owner's laptop had two), so a chat is matched by its own core, not by count.
func windowsCodexDesktopHasCore(ctx context.Context, remote, host string) error {
	cores, err := windowsCodexDesktopCores(ctx, remote)
	if err != nil {
		return err
	}
	for _, core := range cores {
		if core == host {
			return nil
		}
	}
	return errors.New("the Codex core under the desktop pipe is not this chat's Codex process")
}

func windowsCodexDesktopCores(ctx context.Context, remote string) ([]string, error) {
	conn, err := dialCodexDesktop(ctx, remote)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	peer := conn.(*codexWindowsConn).serverPID
	list, err := winProcList()
	if err != nil {
		return nil, err
	}
	serverCreated := winProcCreated(peer)
	var cores []string
	for _, process := range list {
		// The core is the verified pipe server's own codex.exe child. Codex keeps it
		// in the user's LocalAppData, which that user can write, so trust comes from
		// the authenticated parent, not the path. A child created before the server
		// would be a reused PID.
		if process.PPID != int(peer) || !strings.EqualFold(procBaseName(process.Name), "codex") {
			continue
		}
		if _, err := codexWindowsProcessImage(uint32(process.PID)); err != nil {
			continue // another Windows user, or not inspectable
		}
		process.Created = winProcCreated(uint32(process.PID))
		if process.Created == 0 || serverCreated == 0 || process.Created < serverCreated {
			continue
		}
		cores = append(cores, winHostKey(process))
	}
	if len(cores) == 0 {
		return nil, errors.New("Codex desktop core is unavailable")
	}
	return cores, nil
}
