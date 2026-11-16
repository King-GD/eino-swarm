package config

import (
	"os"

	"golang.org/x/sys/windows"
)

func syncRemoteDirectory(path string) (err error) {
	defer func() {
		if err != nil {
			err = &os.PathError{Op: "sync", Path: path, Err: err}
		}
	}()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// FlushFileBuffers requires write access. Go's os.Open directory handle is
	// read-only; os.OpenFile also strips GENERIC_WRITE when opening directories.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := windows.CloseHandle(handle)
		if err == nil {
			err = closeErr
		}
	}()
	return windows.FlushFileBuffers(handle)
}
