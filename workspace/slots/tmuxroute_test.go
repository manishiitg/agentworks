package slots

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseTmuxFindsTheSubcommandPastGlobalFlags(t *testing.T) {
	c := ParseTmux([]string{"-u", "-f", "/x/tmux.conf", "new-session", "-d", "-s", "mlp-1"})
	if c.Subcommand != "new-session" || !c.IsNewSession() || c.ExplicitSocket {
		t.Fatalf("%+v", c)
	}
	if !reflect.DeepEqual(c.Prefix, []string{"-u", "-f", "/x/tmux.conf"}) {
		t.Fatalf("prefix %v", c.Prefix)
	}
	if !ParseTmux([]string{"-L", "mine", "ls"}).ExplicitSocket || !ParseTmux([]string{"-S", "/p", "ls"}).ExplicitSocket {
		t.Fatal("an explicit -L or -S server must be left alone")
	}
	if ParseTmux([]string{"-CC", "attach", "-t", "s"}).Subcommand != "attach" {
		t.Fatal("control-mode flag must not hide the subcommand")
	}
	if !ParseTmux([]string{"ls", "-F", "#{session_name}"}).IsListSessions() {
		t.Fatal("ls is a list command")
	}
}

func TestNewSessionFlags(t *testing.T) {
	c := ParseTmux([]string{"new-session", "-d", "-P", "-F", "#{session_id}", "-s", "mlp-cursor-1", "-c", "/srv/agents/slots/state/slot05/cli-runtimes/v1/abc", "-x", "200", "-y", "50", "env A=1 cursor-agent -c"})
	name, dir := c.NewSessionFlags()
	if name != "mlp-cursor-1" || dir != "/srv/agents/slots/state/slot05/cli-runtimes/v1/abc" {
		t.Fatalf("name %q dir %q", name, dir)
	}
	// a "-c" inside the shell command text must not be read as the start folder
	_, dir = ParseTmux([]string{"new-session", "-d", "-s", "x", "sh -c '-c /etc'"}).NewSessionFlags()
	if dir != "" {
		t.Fatalf("read the folder out of the command text: %q", dir)
	}
}

func TestTargetAndSessionOfTarget(t *testing.T) {
	for in, want := range map[string]string{"mlp-1": "mlp-1", "=mlp-1": "mlp-1", "mlp-1:0.1": "mlp-1", "mlp-1:": "mlp-1", "$3": "", "%7": "", "@2": "", "": ""} {
		if got := SessionOfTarget(in); got != want {
			t.Fatalf("SessionOfTarget(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ParseTmux([]string{"send-keys", "-t", "mlp-1", "-l", "text"}).Target(); got != "mlp-1" {
		t.Fatalf("target %q", got)
	}
	if got := ParseTmux([]string{"send-keys", "--", "-t", "other"}).Target(); got != "" {
		t.Fatalf("text after -- must not be a target: %q", got)
	}
}

func TestSlotOfDirOnlyMatchesRealSlotFoldersUnderTheRoot(t *testing.T) {
	root := "/srv/agents/slots/state"
	if got := SlotOfDir(root, root+"/slot05/cli-runtimes/v1/x"); got != "slot05" {
		t.Fatalf("got %q", got)
	}
	for _, dir := range []string{root, root + "/slot5/x", root + "/notaslot/x", "/srv/agents/state/cli-runtimes/v1/x", root + "/../slot05/x", "/tmp/slot05"} {
		if got := SlotOfDir(root, dir); got != "" {
			t.Fatalf("%q must not map to a slot, got %q", dir, got)
		}
	}
}

func TestSessionFileNeverEscapesTheRegistry(t *testing.T) {
	if SessionFile("/reg", "") != "" {
		t.Fatal("an empty session name has no file")
	}
	for _, name := range []string{"../../etc/passwd", "a/b", "x y;z"} {
		got := SessionFile("/reg", name)
		if !strings.HasPrefix(got, "/reg/") || strings.Contains(strings.TrimPrefix(got, "/reg/"), "/") {
			t.Fatalf("%q -> %q escapes", name, got)
		}
	}
}

func TestNewSessionDetached(t *testing.T) {
	for args, want := range map[string]bool{
		"new-session -d -s x cmd":            true,
		"new-session -dP -s x cmd":           true,
		"new-session -P -d -s x cmd":         true,
		"new-session -s x cmd":               false,
		"new-session -s d cmd":               false, // "d" is a value, not a flag
		"new-session -c /dir/d -s x cmd -d":  false, // after the command text
		"new-session -x 200 -y 50 -d -s x c": true,
	} {
		if got := ParseTmux(strings.Fields(args)).NewSessionDetached(); got != want {
			t.Fatalf("%q: detached %v, want %v", args, got, want)
		}
	}
}

func TestSlotForDirRecognisesAUsersOwnTree(t *testing.T) {
	root := t.TempDir()
	table := filepath.Join(root, "slots.json")
	if err := os.WriteFile(table, []byte(`{"slots":{"slot04":"user-a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ExecConfig{SlotStateRoot: root + "/state", SlotRunRoot: root + "/run", DocsRoot: root + "/docs", SlotTable: table}
	for dir, want := range map[string]string{
		root + "/docs/_users/user-a/Chats/Code/projects/p": "slot04",
		root + "/state/slot07/x":                           "slot07",
		root + "/run/slot02":                               "slot02",
		root + "/docs/_users/user-b/Chats/x":               "", // a user without a slot
		root + "/docs/Workflow/shared":                     "",
		root + "/docs/_users":                              "",
		root + "/docs/_users/../_users/user-a/x":           "slot04",
	} {
		if got := cfg.SlotForDir(dir); got != want {
			t.Fatalf("SlotForDir(%q) = %q, want %q", dir, got, want)
		}
	}
	if (ExecConfig{}).SlotForDir(root+"/docs/_users/user-a/x") != "" {
		t.Fatal("with no docs root configured nothing is a user tree")
	}
}
