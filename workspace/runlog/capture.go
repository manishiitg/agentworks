package runlog

import (
	"io"
	"sync"
	"time"
)

type fragment struct {
	stream string
	data   []byte
	at     time.Time
}

// Capture is eager for a script with an explicit run log. Generic shell calls
// keep only a reply-sized prefix until overflow, timeout or detach needs a file.
// The factory points into an existing private journal, never a project cwd.
type Capture struct {
	mu           sync.Mutex
	open         func() (*Log, error)
	log          *Log
	err          error
	limit        int
	bytes        int
	pending      []fragment
	closed       bool
	maxFileBytes int64
}

func NewCapture(limit int, open func() (*Log, error), eager bool) *Capture {
	c := &Capture{limit: limit, open: open}
	if !eager {
		c.maxFileBytes = 1 << 30
	}
	if eager {
		c.materialize()
	}
	return c
}

func (c *Capture) materialize() {
	if c.log != nil || c.err != nil {
		return
	}
	c.log, c.err = c.open()
	if c.log != nil {
		c.log.maxBytes = c.maxFileBytes
		for _, part := range c.pending {
			c.log.write(part.stream, part.data, part.at)
		}
	}
	c.pending = nil
	c.bytes = 0
}

type captureStream struct {
	capture *Capture
	name    string
}

func (c *Capture) Stream(name string) io.Writer { return captureStream{c, name} }
func (s captureStream) Write(data []byte) (int, error) {
	c := s.capture
	if c == nil {
		return len(data), nil
	}
	at := time.Now().UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return len(data), nil
	}
	if c.log == nil && c.err == nil && c.bytes+len(data) > c.limit {
		c.materialize()
	}
	if c.log != nil {
		c.log.write(s.name, data, at)
	} else if c.err == nil {
		c.pending = append(c.pending, fragment{s.name, append([]byte(nil), data...), at})
		c.bytes += len(data)
	}
	return len(data), nil
}

func (c *Capture) Note(text string) { _, _ = c.Stream("process").Write([]byte(text + "\n")) }
func (c *Capture) Force(note string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.materialize()
	if c.log != nil {
		if !c.closed {
			c.log.Note(note)
		} else {
			c.log.Close()
		}
	}
}
func (c *Capture) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.log != nil {
		c.log.Close()
	}
	// A detach decision can race EOF. Retain the bounded prefix until the
	// caller returns, so Force can still materialize it after Close.
}
func (c *Capture) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.log.Path()
}
func (c *Capture) Error() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err.Error()
	}
	return c.log.Error()
}
func (c *Capture) Complete() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.log != nil && c.closed && c.err == nil && c.log.Error() == ""
}
