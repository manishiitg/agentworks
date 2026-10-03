package browser

import (
	"context"
	"sync"
	"time"
)

// A controller holds the same gate as managed browser commands. Taking control
// never races an in-flight command; later agent commands wait until release.
// Gates are reference counted so finished sessions leave no permanent entries.
type liveGate struct {
	token chan struct{}
	refs  int
}

var liveGates = struct {
	sync.Mutex
	entries map[string]*liveGate
}{entries: map[string]*liveGate{}}

func retainLiveGate(session string) (*liveGate, func()) {
	liveGates.Lock()
	gate := liveGates.entries[session]
	if gate == nil {
		gate = &liveGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		liveGates.entries[session] = gate
	}
	gate.refs++
	liveGates.Unlock()
	return gate, func() {
		liveGates.Lock()
		defer liveGates.Unlock()
		gate.refs--
		if gate.refs == 0 {
			delete(liveGates.entries, session)
		}
	}
}

func AcquireBrowserAutomation(ctx context.Context, session string) (func(), error) {
	if SharedBrowserEnabled() && session == SharedSessionName {
		return func() {}, nil
	}
	gate, drop := retainLiveGate(session)
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-gate.token:
		var once sync.Once
		return func() { once.Do(func() { gate.token <- struct{}{}; drop() }) }, nil
	}
}

func TryTakeBrowserControl(session string) (func(), bool) {
	if SharedBrowserEnabled() && session == SharedSessionName {
		return func() {}, true
	}
	gate, drop := retainLiveGate(session)
	select {
	case <-gate.token:
		var once sync.Once
		return func() { once.Do(func() { gate.token <- struct{}{}; drop() }) }, true
	default:
		drop()
		return nil, false
	}
}

var viewerCDPPorts = struct {
	sync.Mutex
	ports map[string]int
}{ports: map[string]int{}}

// BindViewerCDPPort joins local live control to the same per-port lock used by
// managed CDP actions. The mapping comes from trusted browser startup only.
func BindViewerCDPPort(session string, port int) {
	viewerCDPPorts.Lock()
	defer viewerCDPPorts.Unlock()
	if port > 0 {
		viewerCDPPorts.ports[session] = port
	} else {
		delete(viewerCDPPorts.ports, session)
	}
}

// ViewerCDPPort returns only the endpoint selected by trusted startup.
func ViewerCDPPort(session string) int {
	viewerCDPPorts.Lock()
	defer viewerCDPPorts.Unlock()
	return viewerCDPPorts.ports[session]
}
func TryTakeWorkspaceBrowserControl(session string) (func(), bool) {
	release, ok := TryTakeBrowserControl(session)
	if !ok {
		return nil, false
	}
	viewerCDPPorts.Lock()
	port := viewerCDPPorts.ports[session]
	viewerCDPPorts.Unlock()
	if port == 0 {
		return release, true
	}
	unlock, ok := TryTakeCDPBrowserControl(port)
	if !ok {
		release()
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(func() { unlock(); release() }) }, true
}

// TryTakeCDPBrowserControl also protects startup before a viewer has been bound.
func TryTakeCDPBrowserControl(port int) (func(), bool) {
	if port <= 0 || port > 65535 {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	unlock, err := acquireSharedCDPLock(ctx, port)
	return unlock, err == nil
}
