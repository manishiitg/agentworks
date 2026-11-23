//go:build !darwin && !linux

package sessiontransfer

import "fmt"

func lockMuseSession(_, _ string) (func(), error) {
	return nil, fmt.Errorf("Muse native session moves are available on Mac and Linux")
}
