package clisandbox

import "sync"

// StderrTail keeps the last bytes a CLI wrote to stderr, so a launch that dies at once (a slot launch the launcher
// refused, a server that could not start) reports why instead of a bare exit status or EOF (PLAT-872-86).
type StderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *StderrTail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 1024 {
		t.buf = t.buf[len(t.buf)-1024:]
	}
	return len(b), nil
}

func (t *StderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
