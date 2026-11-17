package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinRM can enable backup/restore privileges, which legitimately bypass a
// DACL during BACKUP_SEMANTICS opens. Change only this disposable test process,
// then restore each previous state before closing its real token handle.
func remoteSyncWithoutBackupPrivileges(t *testing.T, operation func() error) error {
	t.Helper()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_ADJUST_PRIVILEGES, &token); err != nil {
		t.Fatal("cannot inspect/adjust this test process token:", err)
	}
	defer func() {
		if err := token.Close(); err != nil {
			t.Error("cannot close owned test token:", err)
		}
	}()
	readPrivileges := func() []windows.LUIDAndAttributes {
		var size uint32
		if err := windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size); !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			t.Fatal("cannot size this test token's privileges:", err)
		}
		buffer := make([]byte, size)
		if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buffer[0], size, &size); err != nil {
			t.Fatal("cannot inspect this test token's privileges:", err)
		}
		return (*windows.Tokenprivileges)(unsafe.Pointer(&buffer[0])).AllPrivileges()
	}
	for _, name := range []string{"SeBackupPrivilege", "SeRestorePrivilege"} {
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
			t.Fatal(err)
		}
		enabled := false
		for _, privilege := range readPrivileges() {
			if privilege.Luid == luid && privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
				enabled = true
			}
		}
		if !enabled { // Normal users may not have the privilege at all.
			continue
		}
		state := windows.Tokenprivileges{PrivilegeCount: 1}
		state.Privileges[0].Luid = luid
		var previous windows.Tokenprivileges
		var returned uint32
		if err := windows.AdjustTokenPrivileges(token, false, &state, uint32(unsafe.Sizeof(previous)), &previous, &returned); err != nil {
			t.Fatal("cannot disable privileged backup access in this test process:", err)
		}
		defer func() {
			if err := windows.AdjustTokenPrivileges(token, false, &previous, 0, nil, nil); err != nil {
				t.Error("cannot restore owned test token privilege:", err)
			}
			restored := false
			for _, privilege := range readPrivileges() {
				if privilege.Luid == luid && privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
					restored = true
				}
			}
			if !restored {
				t.Error("owned test token privilege was not restored:", name)
			}
		}()
		for _, privilege := range readPrivileges() {
			if privilege.Luid == luid && privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
				t.Fatal("backup privilege remained enabled; denial test environment is unavailable")
			}
		}
	}
	return operation()
}

func TestWindowsRemoteDirectorySyncUsesWritableHandleAndPreservesErrors(t *testing.T) {
	current := remoteTestWindowsSID(t)
	root := t.TempDir()
	safe := remoteTestWindowsPrivateSDDL(current)
	remoteTestWindowsACL(t, root, safe)
	if err := syncRemoteDirectory(root); err != nil {
		t.Fatal("private writable Windows directory could not be flushed:", err)
	}
	file := filepath.Join(root, "not-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := syncRemoteDirectory(file); err != nil {
		t.Fatal("existing regular-file sync compatibility changed:", err)
	}
	for _, tc := range []struct {
		path  string
		cause error
	}{
		{filepath.Join(root, "missing"), os.ErrNotExist},
		{root + "\x00", syscall.EINVAL},
	} {
		err := syncRemoteDirectory(tc.path)
		var pathError *os.PathError
		if !errors.Is(err, tc.cause) || !errors.As(err, &pathError) || pathError.Op != "sync" || pathError.Path != tc.path {
			t.Fatal("directory sync lost its path or original failure:", err)
		}
	}
	denied := filepath.Join(root, "denied")
	if err := os.Mkdir(denied, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remoteTestWindowsACL(t, denied, safe) })
	remoteTestWindowsACL(t, denied, "O:"+current.String()+"D:P(D;;FW;;;"+current.String()+")(A;;FA;;;"+current.String()+")")
	err := remoteSyncWithoutBackupPrivileges(t, func() error { return syncRemoteDirectory(denied) })
	var pathError *os.PathError
	if !errors.Is(err, os.ErrPermission) || !errors.As(err, &pathError) || pathError.Path != denied {
		t.Fatal("denied directory flush was hidden:", err)
	}
}
