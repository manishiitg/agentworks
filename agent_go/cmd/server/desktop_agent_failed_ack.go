package server

import "sync"

const desktopFailedAckLimit = 256

type desktopFailedAckKey struct{ owner, connection, conversation, message string }

// Only permanent local refusals whose failed receipt was explicitly rejected
// by the network enter this process-local, bounded skip set. Successful or
// uncertain sends never do. Physical conversation identity survives reconnects.
type desktopFailedAckCache struct {
	mu    sync.Mutex
	keys  map[desktopFailedAckKey]bool
	order []desktopFailedAckKey
}

func (c *desktopFailedAckCache) contains(key desktopFailedAckKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.keys[key]
}
func (c *desktopFailedAckCache) remember(key desktopFailedAckKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keys[key] {
		return false
	}
	if c.keys == nil {
		c.keys = map[desktopFailedAckKey]bool{}
	}
	if len(c.order) == desktopFailedAckLimit {
		delete(c.keys, c.order[0])
		copy(c.order, c.order[1:])
		c.order[len(c.order)-1] = desktopFailedAckKey{}
		c.order = c.order[:len(c.order)-1]
	}
	c.order = append(c.order, key)
	c.keys[key] = true
	return true
}
