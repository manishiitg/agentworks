// Package runlog streams command output to one timestamped attempt file.
// It does not buffer the full output or change the existing secret policy.
package runlog

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type Log struct {
	mu        sync.Mutex
	file      *os.File
	path      string
	err       error
	closed    bool
	active    string
	openLine  bool
	continued map[string]bool
	maxBytes  int64 // generic spills only; explicit script attempt logs stay uncapped
	written   int64
}

// CreateServerOwned keeps helper logs readable by the slot but writable only
// by the service. The caller anchors and protects the helper log directory.
func CreateServerOwned(root *os.Root, relative string) (*Log, error) {
	log, err := Create(root, relative)
	if err != nil {
		return nil, err
	}
	_, mode, err := wf.ServerFileModes(root)
	if err == nil {
		err = log.file.Chmod(mode)
	}
	if err != nil {
		log.Close()
		return nil, err
	}
	return log, nil
}

// Create is called only after the caller's existing write guard has authorized
// relative. Anchored, symlink-free parents and O_EXCL prevent a log from
// overwriting another file. Redo uses a new attempt name.
func Create(root *os.Root, relative string) (*Log, error) {
	relative, err := wf.CleanRelative(filepath.ToSlash(relative))
	if err != nil || relative == "." {
		return nil, fmt.Errorf("invalid log path")
	}
	parent, err := wf.OpenDirectory(root, filepath.ToSlash(filepath.Dir(relative)), true)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	info, err := parent.Stat(".")
	if err != nil {
		return nil, err
	}
	mode := utils.ProjectFileMode(info)
	file, err := parent.OpenFile(filepath.Base(relative), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, err
	}
	if err = file.Chmod(mode); err != nil {
		file.Close()
		return nil, err
	}
	return &Log{file: file, path: filepath.Join(root.Name(), filepath.FromSlash(relative)), continued: map[string]bool{}}, nil
}

func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
func (l *Log) Error() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err.Error()
	}
	return ""
}

type stream struct {
	log  *Log
	name string
}

func (l *Log) Stream(name string) io.Writer { return stream{l, name} }

// WriteAt retains the original provider-event time rather than the later time
// its callback reaches the log. Stream remains the process-capture interface.
func (l *Log) WriteAt(name string, data []byte, at time.Time) {
	if l == nil {
		return
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	l.write(name, data, at.UTC())
}
func (s stream) Write(data []byte) (int, error) {
	if s.log != nil {
		s.log.write(s.name, data, time.Now().UTC())
	}
	// A persistence error is reported separately; it must never stop draining
	// a process or cause its command to be executed again.
	return len(data), nil
}

func (l *Log) write(name string, data []byte, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.err != nil {
		return
	}
	put := func(b []byte) {
		if l.err == nil {
			if l.maxBytes > 0 && l.written+int64(len(b)) > l.maxBytes {
				remaining := l.maxBytes - l.written
				if remaining > 0 {
					n, err := l.file.Write(b[:remaining])
					l.written += int64(n)
					l.err = err
				}
				if l.err == nil {
					_, l.err = fmt.Fprintf(l.file, "\n%s [process] capture stopped at the %d-byte generic shell log limit; output continues; this file is partial.\n", time.Now().UTC().Format(time.RFC3339Nano), l.maxBytes)
					if l.err == nil {
						l.err = fmt.Errorf("generic shell log reached its %d-byte limit; file is partial", l.maxBytes)
					}
				}
				return
			}
			n, err := l.file.Write(b)
			l.written += int64(n)
			l.err = err
		}
	}
	if l.openLine && l.active != name {
		put([]byte("\n"))
		l.openLine = false
	}
	for len(data) > 0 {
		if !l.openLine {
			label := name
			if l.continued[name] {
				label += " continuation"
			}
			put([]byte(at.Format(time.RFC3339Nano) + " [" + label + "] "))
			l.active = name
			l.openLine = true
		}
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			put(data)
			l.continued[name] = true
			return
		}
		put(data[:i+1])
		data = data[i+1:]
		l.openLine = false
		l.continued[name] = false
	}
}

func (l *Log) Note(text string) {
	if l != nil {
		_, _ = l.Stream("process").Write([]byte(text + "\n"))
	}
}
func (l *Log) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	if err := l.file.Sync(); l.err == nil {
		l.err = err
	}
	if err := l.file.Close(); l.err == nil {
		l.err = err
	}
}
