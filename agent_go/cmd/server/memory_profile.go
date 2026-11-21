package server

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"time"
)

// Automatic profiles for a local machine that slows down. When
// AGENTWORKS_AUTO_PPROF_HEAP_MB is set (the local launcher sets it), the
// minute memory check writes a heap profile, a goroutine dump and a 30 s CPU
// profile into AGENTWORKS_AUTO_PPROF_DIR once the heap passes the limit, at
// most once per autoProfileGap. Agents read the files from disk, so nobody
// has to reach /debug/pprof with a token. Profiles hold stacks and sizes,
// not data or secrets.
const (
	autoProfileGap = 10 * time.Minute
	autoProfileCPU = 30 * time.Second
)

type autoProfiler struct {
	limitMB uint64
	dir     string
	last    time.Time
}

func newAutoProfiler() *autoProfiler {
	limit, err := strconv.ParseUint(os.Getenv("AGENTWORKS_AUTO_PPROF_HEAP_MB"), 10, 64)
	dir := os.Getenv("AGENTWORKS_AUTO_PPROF_DIR")
	if err != nil || limit == 0 || dir == "" {
		return nil
	}
	return &autoProfiler{limitMB: limit, dir: dir}
}

func (p *autoProfiler) check(heapInuseMB uint64, now time.Time) {
	if p == nil || heapInuseMB < p.limitMB || now.Sub(p.last) < autoProfileGap {
		return
	}
	p.last = now
	dir := filepath.Join(p.dir, now.Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[MEM_PROFILE] failed dir=%s err=%v", dir, err)
		return
	}
	write := func(name string, fn func(*os.File) error) {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			log.Printf("[MEM_PROFILE] failed file=%s err=%v", name, err)
			return
		}
		defer f.Close()
		if err := fn(f); err != nil {
			log.Printf("[MEM_PROFILE] failed file=%s err=%v", name, err)
		}
	}
	write("heap.pprof", func(f *os.File) error { return pprof.Lookup("heap").WriteTo(f, 0) })
	write("allocs.pprof", func(f *os.File) error { return pprof.Lookup("allocs").WriteTo(f, 0) })
	write("goroutines.txt", func(f *os.File) error { return pprof.Lookup("goroutine").WriteTo(f, 1) })
	log.Printf("[MEM_PROFILE] wrote dir=%s heap_inuse=%dMB goroutines=%d", dir, heapInuseMB, runtime.NumGoroutine())
	go write("cpu.pprof", func(f *os.File) error {
		if err := pprof.StartCPUProfile(f); err != nil {
			return err // another CPU profile is running
		}
		time.Sleep(autoProfileCPU)
		pprof.StopCPUProfile()
		return nil
	})
}
