package cursorcli

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/fileowner"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// The official CLI's Bash bootstrap would enter the credential-free tool-shell
// layer before Cursor starts (PLAT-856-442). Execute its bundled Node entry in
// the outer CLI layer; Cursor's later tool shells still enter the inner layer.
// Unknown/custom launchers retain their original command.
func cursorNativeEntry(args, toolWrites []string) ([]string, error) {
	fallback := func(reason string) ([]string, error) {
		log.Printf("[CURSOR_NATIVE_ENTRY] mode=bootstrap reason=%s", reason)
		return args, nil
	}
	if len(args) == 0 || (filepath.Base(args[0]) != "cursor-agent" && filepath.Base(args[0]) != "agent") {
		return fallback("unsupported_launcher")
	}
	binary, err := exec.LookPath(args[0])
	if err != nil {
		return fallback("executable_unavailable")
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, err
	}
	// This is the install root managed by AgentWorks' CLI updater, not an
	// arbitrary matching script from another project or account.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	home = pathidentity.Key(home)
	install := filepath.Join(home, ".local", "share", "cursor-agent")
	if pathidentity.Key(install) != install {
		return fallback("linked_install_root")
	}
	root := filepath.Dir(binary)
	if filepath.Dir(filepath.Dir(root)) != install || filepath.Base(filepath.Dir(root)) != "versions" {
		return fallback("unmanaged_installation")
	}
	for _, grant := range toolWrites {
		if grant == "" {
			continue
		}
		grant = pathidentity.Key(grant)
		for _, path := range []string{root, binary, filepath.Join(root, "node"), filepath.Join(root, "index.js")} {
			path = pathidentity.Key(path)
			if path == grant || strings.HasPrefix(path, grant+string(filepath.Separator)) || grant == string(filepath.Separator) {
				return fallback("installation_is_tool_writable")
			}
		}
	}
	f, err := os.Open(binary)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16<<10))
	f.Close()
	if err != nil {
		return nil, err
	}
	shim := string(raw)
	if !strings.HasPrefix(shim, "#!/usr/bin/env bash\n") ||
		!strings.Contains(shim, `NODE_BIN="$SCRIPT_DIR/node"`) ||
		!strings.Contains(shim, `exec -a "$0" "$NODE_BIN"`) ||
		!strings.Contains(shim, `"$SCRIPT_DIR/index.js" "$@"`) {
		return fallback("unrecognized_bootstrap")
	}
	node, entry := filepath.Join(root, "node"), filepath.Join(root, "index.js")
	groupChecked, privateGroup := false, false
	for _, path := range []string{home, filepath.Dir(filepath.Dir(install)), filepath.Dir(install), install, filepath.Dir(root), root, binary, node, entry} {
		info, e := os.Stat(path)
		if e != nil {
			return nil, fmt.Errorf("Cursor's bundled Node entry is unavailable; update the installed Cursor CLI")
		}
		uid, gid, ok := fileowner.IDs(info)
		if !ok || (uid != os.Geteuid() && uid != 0) || info.Mode().Perm()&0002 != 0 {
			return fallback("untrusted_owner_or_world_writable")
		}
		if info.Mode().Perm()&0020 != 0 {
			if gid != os.Getegid() {
				return fallback("untrusted_group")
			}
			if !groupChecked {
				groupChecked, privateGroup = true, cursorAppGroupPrivate()
			}
			if !privateGroup {
				return fallback("shared_install_group")
			}
		}
		if path == binary || path == node || path == entry {
			if !info.Mode().IsRegular() || filepath.Dir(pathidentity.Key(path)) != root || (path == node && info.Mode()&0111 == 0) {
				return nil, fmt.Errorf("Cursor's bundled Node entry is outside its managed installation")
			}
		}
	}
	out := []string{"/usr/bin/env", "CURSOR_INVOKED_AS=" + filepath.Base(args[0]), node}
	// Match the bootstrap's feature check; older bundled Node versions omit
	// this flag. No model request or account file is used by --version.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, node, "--use-system-ca", "--version")
	probe.Env = []string{"PATH=/usr/bin:/bin"}
	if probe.Run() == nil {
		out = append(out, "--use-system-ca")
	}
	out = append(out, entry)
	return append(out, args[1:]...), nil
}

// A group-writable managed install is trusted only when the group contains
// the application account alone, including primary memberships omitted by
// `getent group`. Recheck NSS for each launch decision, not a process cache.
func cursorAppGroupPrivate() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	group, err := exec.CommandContext(ctx, "/usr/bin/getent", "group", strconv.Itoa(os.Getegid())).Output()
	if err != nil {
		return false
	}
	users, err := exec.CommandContext(ctx, "/usr/bin/getent", "passwd").Output()
	return err == nil && cursorPrivateInstallGroup(string(group), string(users), strconv.Itoa(os.Geteuid()), strconv.Itoa(os.Getegid()))
}

func cursorPrivateInstallGroup(group, users, uid, gid string) bool {
	fields := strings.Split(strings.TrimSpace(group), ":")
	if len(fields) != 4 || fields[2] != gid {
		return false
	}
	app := ""
	for _, line := range strings.Split(strings.TrimSpace(users), "\n") {
		user := strings.Split(line, ":")
		if len(user) < 4 {
			return false
		}
		if user[2] == uid {
			app = user[0]
		}
		if user[3] == gid && user[2] != uid {
			return false
		}
	}
	if app == "" {
		return false
	}
	for _, member := range strings.Split(fields[3], ",") {
		if member != "" && member != app {
			return false
		}
	}
	return true
}
