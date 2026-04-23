//go:build !unix

package coding

import "os/exec"

func configureExecutorProcess(cmd *exec.Cmd) {}

func killExecutorProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
