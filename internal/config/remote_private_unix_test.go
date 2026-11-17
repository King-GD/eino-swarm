//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemotePrivateUnixPermissionsKeepGroupAndOtherBitsForbidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0000, 0400, 0600, 0700, 0601, 0610, 0640, 0604, 0644, 0777} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := privateRemotePermissions(path, info); got != (mode&0077 == 0) {
			t.Fatalf("private mode %04o = %v", mode, got)
		}
	}
	if privateRemotePermissions(path, nil) {
		t.Fatal("missing file metadata accepted")
	}
}

func TestRemoteCreatedCredentialIsCheckedBeforeAnyBytesAreWritten(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "credential-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !privateRemoteCreatedFile(file) {
		t.Fatal("private newly created credential refused")
	}
	if err := file.Chmod(0644); err != nil {
		t.Fatal(err)
	}
	if privateRemoteCreatedFile(file) {
		t.Fatal("new public file accepted for credential bytes")
	}
	info, err := file.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("rejected new file should contain no credential bytes")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if privateRemoteCreatedFile(file) || privateRemoteCreatedFile(nil) {
		t.Fatal("closed or absent credential file accepted")
	}
}
