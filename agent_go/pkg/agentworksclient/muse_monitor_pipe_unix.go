//go:build !windows

package agentworksclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
)

func openMuseMonitorPrivate(path string, fifo bool) (*os.File, error) {
	return openMuseMonitorPrivateContext(context.Background(), path, fifo)
}

func openMuseMonitorPrivateContext(ctx context.Context, path string, fifo bool) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || resolved != filepath.Dir(path) {
		return nil, errors.New("linked Muse subscription directory")
	}
	directory, e := os.Lstat(filepath.Dir(path))
	if e != nil || !directory.IsDir() || directory.Mode().Perm() != 0700 || directory.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, errors.New("unsafe Muse subscription directory")
	}
	opened := make(chan struct{})
	if fifo {
		go func() {
			select {
			case <-opened:
				return
			case <-ctx.Done():
			}
			// A single cancellation endpoint releases a blocked reader open,
			// including broker exit between its liveness check and this open.
			fd, e := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
			if e == nil {
				<-opened
				_ = syscall.Close(fd)
			}
		}()
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	close(opened)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		syscall.Close(fd)
		return nil, ctx.Err()
	}
	if fifo {
		if err = syscall.SetNonblock(fd, true); err != nil {
			syscall.Close(fd)
			return nil, err
		}
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || fifo && info.Mode()&os.ModeNamedPipe == 0 || !fifo && !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("unsafe Muse subscription file")
	}
	return file, nil
}

func createMuseMonitorFiles(path string, record MuseMonitorSubscription) error {
	if err := syscall.Mkfifo(path+".fifo", 0600); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		removeMuseMonitorFiles(path)
		return err
	}
	lock.Close()
	if err = atomicLiveJSON(path, record); err != nil {
		removeMuseMonitorFiles(path)
		return err
	}
	return nil
}
func removeMuseMonitorFiles(path string) {
	for _, suffix := range []string{"", ".fifo", ".lock", ".broker"} {
		_ = os.Remove(path + suffix)
	}
}

func cleanupMuseMonitorFiles(dir string) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, path := range paths {
		if _, err := uuid.Parse(filepath.Base(path[:len(path)-len(".json")])); err != nil {
			continue
		}
		if _, err := ReadMuseMonitorSubscription(path); err != nil {
			continue
		}
		lock, err := openMuseMonitorPrivate(path+".broker", false)
		if err != nil {
			continue
		}
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			// A live broker always holds this lock before publishing its record.
			// Delete only that free UUID set, never an active broker's files.
			removeMuseMonitorFiles(path)
		}
		lock.Close()
	}
}

func openMuseMonitorWriter(ctx context.Context, path string) (*os.File, error) {
	opened := make(chan struct{})
	go func() {
		select {
		case <-opened:
			return
		case <-ctx.Done():
		}
		// One cancellation event, not a timed open/reader probe.
		fd, err := syscall.Open(path+".fifo", syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if err == nil {
			<-opened
			_ = syscall.Close(fd)
		}
	}()
	fd, err := syscall.Open(path+".fifo", syscall.O_WRONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	close(opened)
	if err != nil {
		return nil, err
	}
	if err = syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path+".fifo")
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		file.Close()
		return nil, errors.New("unsafe Muse metadata writer")
	}
	return file, nil
}

func waitMuseMonitorReader(path string, cancel context.CancelFunc) {
	lock, err := openMuseMonitorPrivate(path+".lock", false)
	if err != nil {
		cancel()
		return
	}
	defer lock.Close()
	// The reader locks BEFORE opening its FIFO. Kernel lock release observes
	// its exit even with no messages, without polling a PID or inbox.
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err == nil {
		cancel()
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
}

func ReadMuseMonitorPipe(ctx context.Context, path, session, lease string, offer func(MuseWatchHint) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := verifyMuseMonitorBroker(path); err != nil {
		return err
	}
	record, err := ReadMuseMonitorSubscription(path)
	if err != nil {
		return err
	}
	if record.SessionID != session || record.AttachmentID != lease {
		return errors.New("Monitor subscription changed session or lease")
	}
	lock, err := openMuseMonitorPrivate(path+".lock", false)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("this exact Muse subscription already has a reader")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	file, err := openMuseMonitorPrivateContext(ctx, path+".fifo", true)
	if err != nil {
		return err
	}
	defer file.Close()
	go func() { <-ctx.Done(); _ = file.Close() }()
	// macOS never reports end-of-file on a non-blocking FIFO after its writer
	// closes, so a reader of a finished broker generation would linger forever
	// (and Muse would keep listing it as a live Monitor). The broker holds an
	// exclusive lock for exactly its lifetime: wait for the kernel to release it.
	if broker, e := openMuseMonitorPrivate(path+".broker", false); e == nil {
		go func() {
			defer broker.Close()
			if syscall.Flock(int(broker.Fd()), syscall.LOCK_SH) == nil {
				cancel()
			}
		}()
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 4096)
	for scanner.Scan() {
		var hint MuseWatchHint
		if err = json.Unmarshal(scanner.Bytes(), &hint); err != nil {
			return err
		}
		if hint.Source != record.Source || hint.SessionID != session || hint.AttachmentID != lease {
			return errors.New("Monitor pipe changed session or lease")
		}
		if err = offer(hint); err != nil {
			return err
		}
	}
	if err = scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// A held private kernel lock is live-process evidence, not metadata authority.
// A stale record from a crash is refused; newly staged pipes use new UUIDs and
// never replace another broker's files or reuse an old process's subscription.
func holdMuseMonitorBroker(path string) (*os.File, error) {
	file, err := os.OpenFile(path+".broker", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
func verifyMuseMonitorBroker(path string) error {
	file, err := openMuseMonitorPrivate(path+".broker", false)
	if err != nil {
		return err
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return errors.New("Muse subscription has no live broker; get fresh Monitor arguments")
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		return err
	}
	return nil
}
