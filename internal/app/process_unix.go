//go:build !windows

package app

import (
	"os"
	"os/exec"
	"syscall"
)

func configureChild(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateChild(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGTERM)
}

func killChild(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGKILL)
}
