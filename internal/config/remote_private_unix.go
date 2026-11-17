//go:build !windows

package config

import "os"

func privateRemotePermissions(_ string, info os.FileInfo) bool {
	return info != nil && info.Mode().Perm()&0077 == 0
}
