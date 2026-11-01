//go:build windows

package ffmpeg

import (
	"context"
	"os/exec"
	"syscall"
)

const (
	createNoWindow           = 0x08000000
	belowNormalPriorityClass = 0x00004000
)

// Command builds an exec.Cmd for ffmpeg/ffprobe: no console window and below
// normal priority, so a transcode never flashes a window or starves games.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | belowNormalPriorityClass,
	}
	return cmd
}

// Start starts cmd (priority is already set through the creation flags).
func Start(cmd *exec.Cmd) error { return cmd.Start() }
