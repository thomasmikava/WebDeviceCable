//go:build !windows

package bridge

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func startOwnedCommand(cmd *exec.Cmd) (func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(e, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return e
	}
	cmd.Cancel = kill
	if e := cmd.Start(); e != nil {
		return nil, e
	}
	return func() { _ = kill() }, nil
}
