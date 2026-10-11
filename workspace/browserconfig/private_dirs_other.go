//go:build !unix

package browserconfig

import "fmt"

func ensurePrivateDirectories(_ string, _ ...string) error {
	return fmt.Errorf("private deployment browser directories require Unix ownership checks")
}
