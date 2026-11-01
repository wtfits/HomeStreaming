//go:build !windows

package config

// UsersModifyCommand returns the command that makes the data directory
// writable for the server's account.
func UsersModifyCommand(dir string) string {
	return `sudo chown -R "$(id -un)" "` + dir + `"`
}

// GrantUsersModify is a no-op outside Windows (Unix permissions follow the
// creating user; the systemd unit and Docker image own their data dir).
func GrantUsersModify(dir string, recursive bool) error { return nil }
