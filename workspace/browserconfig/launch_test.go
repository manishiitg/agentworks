package browserconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSharedProfileLaunchIdentity(t *testing.T) {
	t.Setenv(ProfileEnv, "/data/browser-profile")
	args := HeadlessArgs()
	if !SharedEnabled() || SharedProfile() != "/data/browser-profile" {
		t.Fatal("shared profile disabled")
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--user-agent") || !strings.Contains(joined, "--profile /data/browser-profile") || !strings.Contains(joined, "--idle-timeout 0") {
		t.Fatalf("wrong shared launch: %v", args)
	}
	if !reflect.DeepEqual(args, HeadlessArgs()) {
		t.Fatal("unstable launch options")
	}
	t.Setenv(ProfileEnv, "")
	if SharedEnabled() || !strings.Contains(strings.Join(HeadlessArgs(), " "), "--user-agent") {
		t.Fatal("isolated behavior changed")
	}
	for _, path := range []string{"relative", "/", "/tmp/.."} {
		t.Setenv(ProfileEnv, path)
		if SharedEnabled() {
			t.Fatalf("unsafe profile %q", path)
		}
	}
}

func TestManagedHeadlessMediaFlags(t *testing.T) {
	for _, profile := range []string{"", "/data/browser-profile"} {
		t.Setenv(ProfileEnv, profile)
		args := HeadlessArgs()
		launch := ""
		for i, arg := range args {
			if arg == "--args" && i+1 < len(args) {
				launch = args[i+1]
			}
		}
		flags := strings.Split(launch, ",")
		for _, want := range []string{"--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"} {
			count := 0
			for _, flag := range flags {
				if flag == want {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("profile %q: expected one %s in %v", profile, want, args)
			}
		}
	}
}

func TestUserBrowserProfilesDoNotShareCookies(t *testing.T) {
	t.Setenv(ProfileEnv, "/data/profile")
	a := "customer--user-0123456789abcdef--browser"
	b := "customer--user-fedcba9876543210--browser"
	for _, session := range []string{a, b} {
		args := HeadlessArgsForSession(session)
		if !IsUserSession(session) || args[1] != "/data/profile-users/"+session {
			t.Fatalf("unsafe profile: %v", args)
		}
	}
	if reflect.DeepEqual(HeadlessArgsForSession(a), HeadlessArgsForSession(b)) {
		t.Fatal("shared user profile")
	}
}

func TestWorkflowBrowserUsesPersistentProfile(t *testing.T) {
	t.Setenv(ProfileEnv, "/data/profile")
	session := "workflow-0123456789abcdef--browser"
	args := HeadlessArgsForSession(session)
	if !IsUserSession(session) || args[1] != "/data/profile-workflows/"+session {
		t.Fatalf("workflow profile was not isolated: %v", args)
	}
}

func TestProductWorkspaceBrowserUsesPersistentIsolatedProfile(t *testing.T) {
	t.Setenv(ProfileEnv, "/data/profile")
	session := "video-prod--workspace-0123456789abcdef--browser"
	args := HeadlessArgsForSession(session)
	if !IsUserSession(session) || args[1] != "/data/profile-users/"+session {
		t.Fatalf("product workspace profile was not isolated: %v", args)
	}
}

func TestProjectBrowserUsesProjectsProfileRoot(t *testing.T) {
	t.Setenv(ProfileEnv, "/data/profile")
	for session, root := range map[string]string{
		"project-0123456789abcdef--browser":             "/data/profile-projects/",
		"video-prod--project-0123456789abcdef--browser": "/data/profile-projects/",
		"session-0123456789abcdef--browser":             "/data/profile-users/",
		"workflow-0123456789abcdef--browser":            "/data/profile-workflows/",
	} {
		if !IsUserSession(session) || ProfilePathForSession(session) != root+session {
			t.Fatalf("%s: profile %q, want under %s", session, ProfilePathForSession(session), root)
		}
	}
}

func TestDefaultScopedProfilesRememberLoginsWithoutGlobalSharing(t *testing.T) {
	previous, had := os.LookupEnv(ProfileEnv)
	os.Unsetenv(ProfileEnv)
	defer func() {
		if had {
			os.Setenv(ProfileEnv, previous)
		} else {
			os.Unsetenv(ProfileEnv)
		}
	}()
	root := t.TempDir()
	t.Setenv(ProfileRootEnv, root)
	a := "workflow-0123456789abcdef--browser"
	b := "project-fedcba9876543210--browser"
	if SharedEnabled() {
		t.Fatal("default scoped profiles enabled global sharing")
	}
	if ProfilePathForSession(a) != filepath.Join(root+"-workflows", a) || ProfilePathForSession(b) != filepath.Join(root+"-projects", b) {
		t.Fatal("scoped profile roots do not match")
	}
	if ProfilePathForSession("default") != "" {
		t.Fatal("unbound browser received shared persistent profile")
	}
}
