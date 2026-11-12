//go:build !windows

package browser

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcess(cmd *exec.Cmd) error {
	// 独立进程组保证超时后连同 Chromium 子进程一起终止。
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	} else if !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return cmd.Process.Kill()
}
