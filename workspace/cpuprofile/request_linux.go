//go:build linux

// Package cpuprofile captures opt-in profiles through a private log-directory
// marker, without a network listener or changing application authentication.
package cpuprofile

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"
)

var once sync.Once

// Start observes pprof/REQUEST next to the process log. Existing markers are
// ignored at startup. Touching the marker again requests one new capture from
// each service; neither service removes it before the other has observed it.
func Start(service string) {
	once.Do(func() {
		file := os.Getenv("LOG_FILE")
		if file == "" {
			file, _ = os.Readlink("/proc/self/fd/1")
		}
		if !filepath.IsAbs(file) {
			return
		}
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		root, err := privateRoot(filepath.Join(filepath.Dir(file), "pprof"))
		if err != nil {
			log.Printf("[CPU_PROFILE] service=%s event=disabled reason=%q", service, err)
			return
		}
		last := request(root)
		log.Printf("[CPU_PROFILE] service=%s event=ready duration_s=30", service)
		go func() {
			defer root.Close()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				next := request(root)
				if next == nil {
					continue
				}
				if last != nil && os.SameFile(last, next) && last.ModTime() == next.ModTime() {
					continue
				}
				last = next
				if err := capture(root, service, 30*time.Second); err != nil {
					log.Printf("[CPU_PROFILE] service=%s event=failed reason=%q", service, err)
				}
			}
		}()
	})
}

func ownedPrivate(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}

func privateRoot(dir string) (*os.Root, error) {
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || !ownedPrivate(before) {
		return nil, fmt.Errorf("profile directory must be private and service-owned")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, fmt.Errorf("profile directory changed")
	}
	return root, nil
}

func request(root *os.Root) os.FileInfo {
	info, err := root.Lstat("REQUEST")
	if err != nil || !info.Mode().IsRegular() || !ownedPrivate(info) {
		return nil
	}
	return info
}

func capture(root *os.Root, service string, duration time.Duration) error {
	// Exclusive creation refuses pre-existing files and symlinks. os.Root anchors
	// all outputs within the admitted private directory.
	prefix := fmt.Sprintf("%s-%d-%s", service, os.Getpid(), time.Now().UTC().Format("20060102T150405.000000000Z"))
	writeAllocs := func(suffix string) error {
		f, err := root.OpenFile(prefix+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		return pprof.Lookup("allocs").WriteTo(f, 0)
	}
	if err := writeAllocs("-allocs-before.pprof"); err != nil {
		return err
	}
	f, err := root.OpenFile(prefix+"-cpu.pprof", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := pprof.StartCPUProfile(f); err != nil {
		return err
	}
	var before, after runtime.MemStats
	var cpuBefore, cpuAfter syscall.Rusage
	runtime.ReadMemStats(&before)
	syscall.Getrusage(syscall.RUSAGE_SELF, &cpuBefore)
	start := time.Now()
	log.Printf("[CPU_PROFILE] service=%s event=started profile=%s duration_s=%.0f", service, prefix, duration.Seconds())
	time.Sleep(duration)
	syscall.Getrusage(syscall.RUSAGE_SELF, &cpuAfter)
	seconds := time.Since(start).Seconds()
	runtime.ReadMemStats(&after)
	pprof.StopCPUProfile()
	if err := writeAllocs("-allocs-after.pprof"); err != nil {
		return err
	}
	cpuSeconds := func(r syscall.Rusage) float64 {
		return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
	}
	log.Printf("[CPU_PROFILE] service=%s event=done profile=%s window_seconds=%.3f cpu_percent=%.2f gc_per_minute=%.2f alloc_bytes=%d mallocs=%d", service, prefix, seconds, 100*(cpuSeconds(cpuAfter)-cpuSeconds(cpuBefore))/seconds, 60*float64(after.NumGC-before.NumGC)/seconds, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	return nil
}
