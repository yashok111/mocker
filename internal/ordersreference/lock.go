package ordersreference

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// The lock file is retained: unlinking it would let another process lock a new inode.
func lockDB(path string) (*os.File, error) {
	fd, err := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path+".lock")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("Orders database already in use")
	}
	return f, nil
}
func unlockDB(f *os.File) {
	if f != nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}
}
