//go:build darwin || linux

package sessiontransfer

import (
	"fmt"
	"os"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"golang.org/x/sys/unix"
)

// Hold Muse's own session lock through ancestor validation and replacement.
// An active CLI (including one outside this adapter) must release it first.
func lockMuseSession(dataHome, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("Muse is still using this session; finish its reply and close its terminal before moving it")
	}
	if err = slotfs.ShareTree(dataHome, path); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
