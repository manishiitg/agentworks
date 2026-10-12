//go:build !windows

package main

import (
	"errors"
	"io"
	"os"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// Hold the validated directory descriptor throughout each operation. NOFOLLOW
// and openat prevent path replacement from redirecting reads/writes elsewhere.
func openMuseObservationDirectory(path string, create bool) (*os.File, error) {
	if create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0777 != 0700 {
		dir.Close()
		return nil, errors.New("Muse discovery needs an owned 0700 directory")
	}
	return dir, nil
}

func readMuseObservation(dir *os.File, name string) ([]byte, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0777 != 0600 || stat.Size > 64<<10 {
		return nil, errors.New("Muse discovery needs an owned regular 0600 record")
	}
	return io.ReadAll(io.LimitReader(f, 64<<10))
}

func writeMuseObservation(dir *os.File, name string, b []byte) error {
	temp := ".observation-" + uuid.NewString()
	fd, err := unix.Openat(int(dir.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer removeMuseObservation(dir, temp)
	f := os.NewFile(uintptr(fd), temp)
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return unix.Renameat(int(dir.Fd()), temp, int(dir.Fd()), name)
}

func removeMuseObservation(dir *os.File, name string) {
	_ = unix.Unlinkat(int(dir.Fd()), name, 0)
}
