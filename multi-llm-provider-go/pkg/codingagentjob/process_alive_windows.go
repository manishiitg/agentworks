//go:build windows

package codingagentjob

func isProcessAlive(pid int) bool {
	return pid > 0
}
