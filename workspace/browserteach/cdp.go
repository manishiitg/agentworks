// Package browserteach records demonstrations in an existing browser. It never
// launches Chrome and its private CDP connection is not exposed through the UI.
package browserteach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type Message struct {
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Session string          `json:"sessionId,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
type Connection struct {
	socket  *websocket.Conn
	write   sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan Message
	next    atomic.Int64
	Events  chan Message
	done    chan struct{}
	once    sync.Once
}

func Connect(ctx context.Context, endpoint string) (*Connection, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "ws" || u.User != nil {
		return nil, errors.New("invalid private browser endpoint")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("browser recorder requires a loopback endpoint")
	}
	ws, _, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c := &Connection{socket: ws, pending: map[int64]chan Message{}, Events: make(chan Message, 512), done: make(chan struct{})}
	ws.SetReadLimit(8 << 20)
	go c.read()
	return c, nil
}
func (c *Connection) Close() { c.once.Do(func() { close(c.done); c.socket.Close() }) }
func (c *Connection) read() {
	defer c.Close()
	defer close(c.Events)
	for {
		var m Message
		if c.socket.ReadJSON(&m) != nil {
			return
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		select {
		case c.Events <- m:
		default:
			return
		}
	}
}
func (c *Connection) Call(ctx context.Context, session, method string, params any, out any) error {
	id := c.next.Add(1)
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	data, _ := json.Marshal(params)
	c.write.Lock()
	c.socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := c.socket.WriteJSON(Message{ID: id, Session: session, Method: method, Params: data})
	c.write.Unlock()
	if err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("browser recorder disconnected")
	}
}
