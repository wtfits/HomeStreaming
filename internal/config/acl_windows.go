//go:build windows

package config

import (
	"os/exec"
	"syscall"
)

// UsersModifyCommand is the icacls invocation that lets every local user
// modify the data directory. Without it, a database created by one account
// (an elevated prompt or the LocalSystem service) is read-only for another.
func UsersModifyCommand(dir string) string {
	return `icacls "` + dir + `" /grant "*S-1-5-32-545:(OI)(CI)M" /T`
}

// GrantUsersModify grants BUILTIN\Users modify rights (inherited) on dir.
// recursive also fixes files that already exist. Best effort.
func GrantUsersModify(dir string, recursive bool) error {
	args := []string{dir, "/grant", "*S-1-5-32-545:(OI)(CI)M", "/C", "/Q"}
	if recursive {
		args = append(args, "/T")
	}
	cmd := exec.Command("icacls", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Run()
}
