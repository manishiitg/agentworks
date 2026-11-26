package server

import (
	"fmt"
	"log"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// Keep this Mac awake for workflows (#3617, owner-approved mockup). A schedule chooses it; it is off by default.
const (
	keepAwakeWhileRunning = "while_running" // awake while one of this schedule's runs is going
	keepAwakeUpcoming     = "upcoming"      // also awake so upcoming runs are not missed
)

// keepAwakeState is what the Mac app applies: hold a power-save blocker, and only while plugged in unless a
// schedule that wants it allows battery.
type keepAwakeState struct {
	Hold          bool `json:"hold"`
	PluggedInOnly bool `json:"plugged_in_only"`
}

// keepAwakeHub is the server's single answer to "should this Mac stay awake now". The scheduler reports running
// runs and enabled "upcoming" schedules; the Mac app's notify stream receives every change.
type keepAwakeHub struct {
	mu       sync.Mutex
	runs     map[string]bool            // run ID -> may stay awake on battery
	upcoming map[string]map[string]bool // source (crew timers, workflow schedules) -> schedule key -> may stay awake on battery
	subs     map[chan keepAwakeState]struct{}
	last     keepAwakeState
}

var desktopKeepAwake = &keepAwakeHub{runs: map[string]bool{}, upcoming: map[string]map[string]bool{}, subs: map[chan keepAwakeState]struct{}{}}

// holdRun keeps the Mac awake while this run is going when its schedule asks for it. Only the Mac app's own server
// has a Mac to keep awake.
func (h *keepAwakeHub) holdRun(runID string, schedule WorkflowSchedule) func() {
	if !IsLocalMode() || runID == "" || (schedule.KeepAwake != keepAwakeWhileRunning && schedule.KeepAwake != keepAwakeUpcoming) {
		return func() {}
	}
	h.mu.Lock()
	h.runs[runID] = schedule.KeepAwakeOnBattery
	h.publishLocked()
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.runs, runID)
		h.publishLocked()
		h.mu.Unlock()
	}
}

// setUpcoming replaces one source's enabled schedules that keep the Mac awake for upcoming runs.
func (h *keepAwakeHub) setUpcoming(source string, schedules map[string]bool) {
	if !IsLocalMode() {
		return
	}
	h.mu.Lock()
	h.upcoming[source] = schedules
	h.publishLocked()
	h.mu.Unlock()
}

func (h *keepAwakeHub) currentLocked() keepAwakeState {
	state := keepAwakeState{PluggedInOnly: true}
	for _, onBattery := range h.runs {
		state.Hold = true
		state.PluggedInOnly = state.PluggedInOnly && !onBattery
	}
	for _, schedules := range h.upcoming {
		for _, onBattery := range schedules {
			state.Hold = true
			state.PluggedInOnly = state.PluggedInOnly && !onBattery
		}
	}
	return state
}

func (h *keepAwakeHub) publishLocked() {
	next := h.currentLocked()
	if next == h.last {
		return
	}
	h.last = next
	log.Printf("[KEEP_AWAKE] hold=%t plugged_in_only=%t runs=%d", next.Hold, next.PluggedInOnly, len(h.runs))
	for ch := range h.subs {
		select {
		case ch <- next:
		default:
			// A slow listener gets the newest state on its next read.
			select {
			case <-ch:
			default:
			}
			ch <- next
		}
	}
}

// listen returns the current state and a channel of changes.
func (h *keepAwakeHub) listen() (keepAwakeState, chan keepAwakeState, func()) {
	ch := make(chan keepAwakeState, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	state := h.currentLocked()
	h.mu.Unlock()
	return state, ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// validateKeepAwake: only a workflow timer keeps the Mac awake, with one of the two choices.
func validateKeepAwake(schedule productschedule.Schedule) error {
	switch schedule.KeepAwake {
	case "":
		return nil
	case keepAwakeWhileRunning, keepAwakeUpcoming:
		if schedule.WorkflowID == "" {
			return fmt.Errorf("keep_awake is for a workflow schedule")
		}
		return nil
	}
	return fmt.Errorf("keep_awake must be empty, while_running or upcoming")
}
