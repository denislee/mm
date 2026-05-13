//go:build unix

package cache

import (
	"os"
	"syscall"
)

// lockFile acquires an exclusive advisory flock on path, creating the lock
// file if needed. The returned closure releases the lock and closes the fd.
// Blocks until the lock can be taken, so concurrent writers across instances
// serialise.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
