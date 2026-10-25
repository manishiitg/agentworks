package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Several desktop core processes can remain alive after an app restart. Bind
// discovery to the core under the actual IPC server, which also launches MCP.
func codexDesktopProcessHost(ctx context.Context, remote string) (string, error) {
	if !strings.HasPrefix(remote, "desktop+unix://") {
		return "", errors.New("native desktop socket required")
	}
	if err := validateCodexRemote(remote); err != nil {
		return "", err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", strings.TrimPrefix(remote, "desktop+unix://"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return "", err
	}
	var peer int
	var peerErr error
	if err = raw.Control(func(fd uintptr) { peer, peerErr = localSocketPeerPID(int(fd)) }); err != nil {
		return "", err
	}
	if peerErr != nil {
		return "", peerErr
	}
	ps, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,uid=,args=").Output()
	if err != nil {
		return "", err
	}
	return codexCoreHostForPeer(string(ps), peer, func(pid int) (string, error) { return providerPIDHostKey("codex", pid) })
}

func codexCoreHostForPeer(ps string, peer int, key func(int) (string, error)) (string, error) {
	var host string
	for _, line := range strings.Split(ps, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !codexDesktopHost(line) {
			continue
		}
		parent, _ := strconv.Atoi(fields[1])
		uid, uidErr := strconv.Atoi(fields[2])
		appServer := false
		for _, field := range fields[4:] {
			appServer = appServer || field == "app-server"
		}
		if parent != peer || uidErr != nil || uid != os.Getuid() || !appServer {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		candidate, err := key(pid)
		if err != nil || candidate == "" {
			continue
		}
		if host != "" {
			return "", errors.New("desktop has more than one local core")
		}
		host = candidate
	}
	if host == "" {
		return "", errors.New("desktop core unavailable")
	}
	return host, nil
}
