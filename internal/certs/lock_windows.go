//go:build windows

package certs

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func acquireDirectoryLock(dir string) (func(), error) {
	lock, err := os.OpenFile(filepath.Join(dir, ".ca.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open certificate authority lock: %w", err)
	}
	var overlapped windows.Overlapped
	handle := windows.Handle(lock.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock certificate authority directory: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
		_ = lock.Close()
	}, nil
}
