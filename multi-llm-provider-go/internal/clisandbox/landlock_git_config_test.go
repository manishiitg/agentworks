package clisandbox

import (
	"reflect"
	"testing"
)

// The sandbox adds its git protection after the git settings a session already carries (a connected GitHub
// account's credential helper at index 0): neither may replace the other.
func TestAppendGitConfigKeepsEarlierSettings(t *testing.T) {
	session := []string{"PATH=/usr/bin", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.https://github.com.helper", "GIT_CONFIG_VALUE_0=!helper"}
	got := appendGitConfig(session, "safe.directory", "*", "core.hooksPath", "/dev/null", "core.fsmonitor", "false")
	want := []string{
		"GIT_CONFIG_KEY_1=safe.directory", "GIT_CONFIG_VALUE_1=*",
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2=/dev/null",
		"GIT_CONFIG_KEY_3=core.fsmonitor", "GIT_CONFIG_VALUE_3=false",
		"GIT_CONFIG_COUNT=4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appended = %v, want %v", got, want)
	}
	if got := appendGitConfig(nil, "core.hooksPath", "/dev/null"); !reflect.DeepEqual(got, []string{"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null", "GIT_CONFIG_COUNT=1"}) {
		t.Fatalf("appended to an empty env = %v", got)
	}
}
