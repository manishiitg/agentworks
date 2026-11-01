package nativersync

import "os/exec"

func isolateProcess(cmd *exec.Cmd) {}
func stopProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
