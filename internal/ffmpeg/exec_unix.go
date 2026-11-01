//go:build !windows

package ffmpeg

import (
	"context"
	"os/exec"
	"syscall"
)

// Command builds an exec.Cmd for ffmpeg/ffprobe (no shell involved).
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// Start starts cmd and lowers its scheduling priority (nice +10) so playback
// transcodes never starve the rest of the machine.
func Start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
	return nil
}
