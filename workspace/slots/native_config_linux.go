//go:build linux

package slots

import (
	"fmt"
	"os"
	"syscall"
)

// LoadNativeExecConfig proves integrity before trusting readable owner IDs.
// Only root can replace this policy in the root-owned launcher directory.
func LoadNativeExecConfig(path string) (ExecConfig, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return ExecConfig{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return ExecConfig{}, fmt.Errorf("native session policy must be a root-owned file without group or other write access")
	}
	return LoadExecConfig(path)
}
