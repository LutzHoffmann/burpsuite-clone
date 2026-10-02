//go:build !windows

package certs

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireDirectoryLock(dir string) (func(), error) {
	lock, err := os.OpenFile(filepath.Join(dir, ".ca.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open certificate authority lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock certificate authority directory: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}, nil
}
