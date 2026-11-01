// Package nativersync carries rsync's binary protocol over an already
// authenticated stream. It never accepts commands or filesystem paths from a peer.
package nativersync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// Capacity is operational configuration, never selected by the remote peer.
func MaxBytes() int64 {
	if n, e := strconv.ParseInt(os.Getenv("AGENTWORKS_NATIVE_SYNC_MAX_BYTES"), 10, 64); e == nil && n >= 1<<20 && n <= 32<<30 {
		return n
	}
	return 2 << 30
}

// Socket uses binary frames for bytes and one text frame for stream EOF.
// Each direction has one writer; there are no credentials in this stream.
type Socket struct {
	Conn           *websocket.Conn
	reader         io.Reader
	Sent, Received int64
}

func (s *Socket) Read(p []byte) (int, error) {
	for {
		if s.reader != nil {
			n, e := s.reader.Read(p)
			s.Received += int64(n)
			if e == io.EOF {
				s.reader = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, e
		}
		kind, r, e := s.Conn.NextReader()
		if e != nil {
			return 0, e
		}
		if kind == websocket.TextMessage {
			return 0, io.EOF
		}
		if kind != websocket.BinaryMessage {
			return 0, fmt.Errorf("invalid sync frame")
		}
		s.reader = r
	}
}
func (s *Socket) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		size := len(p)
		if size > 64<<10 {
			size = 64 << 10
		}
		if e := s.Conn.WriteMessage(websocket.BinaryMessage, p[:size]); e != nil {
			return n, e
		}
		n += size
		s.Sent += int64(size)
		p = p[size:]
	}
	return n, nil
}
func (s *Socket) CloseWrite() error { return s.Conn.WriteMessage(websocket.TextMessage, []byte("eof")) }

func Binary() (string, error) {
	candidates := []string{os.Getenv("AGENTWORKS_RSYNC_BIN")}
	if executable, e := os.Executable(); e == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "rsync"), filepath.Join(filepath.Dir(executable), "bin", "rsync"))
	}
	if path, e := exec.LookPath("rsync"); e == nil {
		candidates = append(candidates, path)
	}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		raw, e := exec.Command(path, "--version").Output()
		if e != nil {
			continue
		}
		var major, minor, patch int
		if _, e = fmt.Sscanf(string(raw), "rsync version %d.%d.%d", &major, &minor, &patch); e == nil && (major > 3 || major == 3 && minor >= 4) {
			return path, nil
		}
	}
	return "", fmt.Errorf("native session sync needs the AgentWorks rsync helper (version 3.4 or newer)")
}

// Command confines both sender and receiver independently of rsync's path
// checks. stage contains only exported journals, never the live CLI home.
func Command(ctx context.Context, stage string, args []string, extraRead []string) (*exec.Cmd, func(), error) {
	binary, e := Binary()
	if e != nil {
		return nil, nil, e
	}
	stage, e = filepath.EvalSymlinks(stage)
	if e != nil {
		return nil, nil, e
	}
	control, e := os.MkdirTemp("", "agentworks-rsync-policy-")
	if e != nil {
		return nil, nil, e
	}
	cleanup := func() { os.RemoveAll(control) }
	argv := append([]string{binary}, args...)
	switch runtime.GOOS {
	case "darwin":
		var profile strings.Builder
		profile.WriteString("(version 1)(deny default)(allow process-exec)(allow process-fork)(allow signal (target same-sandbox))(allow sysctl-read)(allow mach-lookup)(allow file-read-metadata)(allow file-map-executable)(allow file-read-data (literal \"/\"))")
		for _, p := range append([]string{"/usr", "/bin", "/System", "/Library/Apple", "/private/var/db/dyld", "/private/preboot/Cryptexes", "/dev", binary, stage}, extraRead...) {
			if canonical, e := filepath.EvalSymlinks(p); e == nil {
				p = canonical
			}
			fmt.Fprintf(&profile, "(allow file-read* (subpath %s))", strconv.Quote(p))
			for parent := filepath.Dir(p); parent != "/"; parent = filepath.Dir(parent) {
				fmt.Fprintf(&profile, "(allow file-read-data (literal %s))", strconv.Quote(parent))
			}
		}
		fmt.Fprintf(&profile, "(allow file-write* (subpath %s))(allow file-write* (literal \"/dev/null\"))(allow network-outbound (remote unix-socket))", strconv.Quote(stage))
		p := filepath.Join(control, "profile.sb")
		if e = os.WriteFile(p, []byte(profile.String()), 0600); e != nil {
			cleanup()
			return nil, nil, e
		}
		argv = append([]string{"/usr/bin/sandbox-exec", "-f", p}, argv...)
	case "linux":
		runner, ok := security.CLILandlockRunner()
		if !ok {
			cleanup()
			return nil, nil, fmt.Errorf("native session sync needs filesystem confinement")
		}
		policy := security.LandlockPolicy{ReadPaths: append([]string{binary}, extraRead...), WritePaths: []string{stage}, WorkDir: stage, BrowserScoped: true, StagingOnly: true}
		raw, _ := json.Marshal(policy)
		p := filepath.Join(control, "policy.json")
		if e = os.WriteFile(p, raw, 0600); e != nil {
			cleanup()
			return nil, nil, e
		}
		argv = append([]string{runner, "--config", p, "--"}, argv...)
	default:
		cleanup()
		return nil, nil, fmt.Errorf("native session sync is not available on this operating system")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	isolateProcess(cmd)
	cmd.Dir = stage
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + stage, "TMPDIR=" + stage}
	return cmd, cleanup, nil
}

// Server's flags and root are entirely application-selected. Both ends run
// the same protocol version, independent of the host's default rsync.
func Server(ctx context.Context, stage string, sender bool, stream *Socket) error {
	var err error
	stage, err = filepath.EvalSymlinks(stage)
	if err != nil {
		return err
	}
	args := []string{"--server", "-tre.iLsfxCIvu", "--append", "--append", "--max-size=" + strconv.FormatInt(MaxBytes(), 10)}
	if sender {
		args = append(args, "--sender")
	}
	args = append(args, ".", stage+string(filepath.Separator))
	cmd, cleanup, e := Command(ctx, stage, args, nil)
	if e != nil {
		return e
	}
	defer cleanup()
	return run(ctx, cmd, stream, stage)
}

func run(ctx context.Context, cmd *exec.Cmd, stream *Socket, stage string) error {
	input, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		return e
	}
	defer stopProcess(cmd)
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ctx.Done():
				stopProcess(cmd)
				return
			case <-tick.C:
				var size int64
				filepath.WalkDir(stage, func(_ string, d os.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if !d.IsDir() {
						if i, e := d.Info(); e == nil {
							size += i.Size()
						}
					}
					if size > MaxBytes()*2 {
						return fmt.Errorf("quota")
					}
					return nil
				})
				if size > MaxBytes()*2 {
					stopProcess(cmd)
					return
				}
			}
		}
	}()
	go func() { io.Copy(input, stream); input.Close() }()
	_, copyErr := io.Copy(stream, output)
	e = cmd.Wait()
	stream.CloseWrite()
	if copyErr != nil {
		return copyErr
	}
	if e != nil {
		return fmt.Errorf("rsync failed: %w: %s", e, stderr.String())
	}
	return nil
}

// Client uses a private Unix socket as rsync's remote-shell byte pipe. The
// helper ignores all rsync-supplied argv; it cannot execute a remote command.
func Client(ctx context.Context, stage string, upload bool, stream *Socket) error {
	var err error
	stage, err = filepath.EvalSymlinks(stage)
	if err != nil {
		return err
	}
	control, e := os.MkdirTemp("", "agentworks-rsync-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(control)
	socket := filepath.Join(control, "stream.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		return e
	}
	defer listener.Close()
	helper := filepath.Join(control, "pipe")
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexec " + quote(executable) + " native-sync-pipe " + quote(socket) + "\n"
	if e = os.WriteFile(helper, []byte(script), 0700); e != nil {
		return e
	}
	args := []string{"-rt", "--append-verify", "--protocol=31", "--max-size=" + strconv.FormatInt(MaxBytes(), 10), "-e", helper}
	if upload {
		args = append(args, stage+"/", "peer:.")
	} else {
		args = append(args, "peer:.", stage+"/")
	}
	cmd, cleanup, e := Command(ctx, stage, args, []string{control, executable, filepath.Join(filepath.Dir(executable), "lib")})
	if e != nil {
		return e
	}
	defer cleanup()
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		return e
	}
	defer stopProcess(cmd)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		c, e := listener.(*net.UnixListener).AcceptUnix()
		if e == nil {
			accepted <- c
		} else {
			accepted <- nil
		}
	}()
	var conn *net.UnixConn
	select {
	case conn = <-accepted:
	case err := <-exited:
		return fmt.Errorf("rsync helper failed: %v: %s", err, stderr.String())
	case <-ctx.Done():
		return ctx.Err()
	}
	if conn == nil {
		return fmt.Errorf("rsync helper did not connect")
	}
	defer conn.Close()
	go func() { io.Copy(conn, stream); conn.CloseWrite() }()
	_, copyErr := io.Copy(stream, conn)
	stream.CloseWrite()
	e = <-exited
	if copyErr != nil {
		return copyErr
	}
	if e != nil {
		return fmt.Errorf("rsync failed: %w: %s", e, stderr.String())
	}
	return nil
}

// RunPipe is an internal entry point in the app executable. Unlike platform
// netcat variants it always propagates stdin EOF as a Unix-socket half-close.
func RunPipe(args []string) bool {
	if len(args) == 0 || args[0] != "native-sync-pipe" {
		return false
	}
	if len(args) != 2 || !filepath.IsAbs(args[1]) {
		fmt.Fprintln(os.Stderr, "invalid native sync pipe")
		os.Exit(1)
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: args[1], Net: "unix"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "native sync pipe unavailable")
		os.Exit(1)
	}
	defer c.Close()
	go func() { io.Copy(c, os.Stdin); c.CloseWrite() }()
	if _, err = io.Copy(os.Stdout, c); err != nil {
		os.Exit(1)
	}
	return true
}
