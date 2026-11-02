//go:build !linux

package slots

import "fmt"

func LoadNativeExecConfig(string) (ExecConfig, error) {
	return ExecConfig{}, fmt.Errorf("assigned accounts are only available on Linux")
}
