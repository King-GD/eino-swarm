//go:build unix

package fofaworld

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func runCommand(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err := cmd.Wait()
	stopErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(stopErr, syscall.ESRCH) {
		stopErr = nil
	}
	return errors.Join(err, stopErr, ctx.Err())
}
