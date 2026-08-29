//go:build windows

package app

import (
	"os"
	"os/exec"
)

func configureChild(_ *exec.Cmd) {}

func terminateChild(process *os.Process) error {
	return process.Kill()
}

func killChild(process *os.Process) error {
	return process.Kill()
}
