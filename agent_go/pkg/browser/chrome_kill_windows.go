//go:build windows

package browser

import (
	"os/exec"
	"strconv"
)

// killChromeTree ends Chrome and every helper process it started; Windows has
// no process-group signal, so taskkill walks the tree.
func killChromeTree(chromePID int) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(chromePID)).Run()
}
