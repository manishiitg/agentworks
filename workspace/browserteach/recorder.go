package browserteach

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed recorder.js
var script string

//go:embed locator.js
var locatorScript string

func LocatorExpression(target Target) string {
	data, _ := json.Marshal(target)
	return strings.Replace(locatorScript, "__TARGET_JSON__", string(data), 1)
}

type Target struct {
	Selector string `json:"selector,omitempty"`
	Role     string `json:"role,omitempty"`
	Name     string `json:"name,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Context  string `json:"context,omitempty"`
}
type Action struct {
	ID         int       `json:"id"`
	Kind       string    `json:"kind"`
	Target     Target    `json:"target,omitempty"`
	Value      string    `json:"value,omitempty"`
	URL        string    `json:"url,omitempty"`
	Page       string    `json:"page"`
	Frame      string    `json:"frame,omitempty"`
	At         time.Time `json:"at"`
	Screenshot string    `json:"screenshot,omitempty"`
	Parameter  string    `json:"parameter,omitempty"`
	Frames     []string  `json:"frames,omitempty"`
	Warning    string    `json:"warning,omitempty"`
	Opener     string    `json:"opener,omitempty"`
	Observed   bool      `json:"observed,omitempty"`
}
type Recorder struct {
	mu               sync.Mutex
	attachMu         sync.Mutex
	known            map[string]bool
	declared         map[string]bool
	metadata         map[string]PageInfo
	manualClose      map[string]bool
	manualNavigation map[string]bool
	conn             *Connection
	pages            map[string]string
	frames           map[string]string
	contexts         map[string]map[int64]string
	scripts          map[string]string
	Actions          []Action
	Errors           []string
	Paused           bool
	directory        string
	stopped          bool
	initial          string
	complete         chan struct{}
	evidenceBytes    int64
}

func SanitizeURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func Start(ctx context.Context, endpoint, targetID, dir string) (*Recorder, error) {
	c, err := Connect(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	r := &Recorder{conn: c, pages: map[string]string{}, frames: map[string]string{}, contexts: map[string]map[int64]string{}, scripts: map[string]string{}, directory: dir, initial: targetID, complete: make(chan struct{})}
	r.known = map[string]bool{}
	r.declared = map[string]bool{targetID: true}
	r.metadata = map[string]PageInfo{}
	r.manualClose = map[string]bool{}
	r.manualNavigation = map[string]bool{}
	pages, err := c.Pages(ctx)
	if err != nil {
		c.Close()
		return nil, err
	}
	for _, page := range pages {
		r.known[page.ID] = true
	}
	go r.events()
	if err = r.attach(ctx, targetID); err == nil {
		err = c.Call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return r, nil
}
func (r *Recorder) attach(ctx context.Context, target string) error {
	r.attachMu.Lock()
	defer r.attachMu.Unlock()
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return fmt.Errorf("Teaching has stopped")
	}
	for _, existing := range r.pages {
		if existing == target {
			r.mu.Unlock()
			return nil
		}
	}
	if len(r.pages) >= 12 {
		r.mu.Unlock()
		return fmt.Errorf("Teaching supports up to twelve open tabs")
	}
	r.mu.Unlock()
	var info struct {
		Info PageInfo `json:"targetInfo"`
	}
	if err := r.conn.Call(ctx, "", "Target.getTargetInfo", map[string]any{"targetId": target}, &info); err != nil {
		return err
	}
	if info.Info.Type != "page" {
		return fmt.Errorf("Not a browser page")
	}
	r.mu.Lock()
	if _, ok := r.metadata[target]; !ok {
		info.Info.URL = SanitizeURL(info.Info.URL)
		r.metadata[target] = info.Info
	}
	r.mu.Unlock()
	var attached struct {
		Session string `json:"sessionId"`
	}
	if err := r.conn.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, &attached); err != nil {
		return err
	}
	s := attached.Session
	r.mu.Lock()
	r.pages[s] = target
	r.contexts[s] = map[int64]string{}
	r.mu.Unlock()
	for _, m := range []string{"Runtime.enable", "Page.enable"} {
		if err := r.conn.Call(ctx, s, m, map[string]any{}, nil); err != nil {
			return err
		}
	}
	if err := r.conn.Call(ctx, s, "Runtime.addBinding", map[string]any{"name": "__awTeachEvent"}, nil); err != nil {
		return err
	}
	var init struct {
		Identifier string `json:"identifier"`
	}
	if err := r.conn.Call(ctx, s, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": script}, &init); err != nil {
		return err
	}
	r.mu.Lock()
	r.scripts[s] = init.Identifier
	r.mu.Unlock()
	var tree struct {
		Tree struct {
			Frame struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := r.conn.Call(ctx, s, "Page.getFrameTree", map[string]any{}, &tree); err != nil {
		return err
	}
	r.mu.Lock()
	r.frames[tree.Tree.Frame.ID] = SanitizeURL(tree.Tree.Frame.URL)
	r.mu.Unlock()
	// Existing default execution contexts include same-process child frames.
	r.mu.Lock()
	var ids []int64
	for id := range r.contexts[s] {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	r.mu.Lock()
	command := "reset"
	if r.Paused {
		command = "pause"
	}
	r.mu.Unlock()
	expression := script + ";window.__awTeachControl?.(" + fmt.Sprintf("%q", command) + ")"
	for _, id := range ids {
		_ = r.conn.Call(ctx, s, "Runtime.evaluate", map[string]any{"expression": expression, "contextId": id}, nil)
	}
	_ = r.conn.Call(ctx, s, "Runtime.evaluate", map[string]any{"expression": expression}, nil)
	if target == r.initial {
		r.add(Action{Kind: "navigate", Page: target, Frame: tree.Tree.Frame.ID, URL: SanitizeURL(tree.Tree.Frame.URL)})
	}
	return nil
}
func (r *Recorder) add(a Action) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Paused || r.stopped {
		return 0
	}
	if len(r.Actions) >= 1000 || (a.Page != "" && !r.declared[a.Page] && len(r.Actions) >= 999) {
		r.Errors = append(r.Errors, "Action limit reached; demonstration is incomplete")
		r.Paused = true
		return 0
	}
	if a.Page != "" && !r.declared[a.Page] {
		meta := r.metadata[a.Page]
		r.declared[a.Page] = true
		_, openerTracked := r.declared[meta.Opener]
		r.Actions = append(r.Actions, Action{ID: len(r.Actions) + 1, Kind: "tab_open", Page: a.Page, URL: SanitizeURL(meta.URL), Opener: meta.Opener, Observed: openerTracked && meta.Observed, At: time.Now().UTC()})
	}
	a.ID = len(r.Actions) + 1
	a.At = time.Now().UTC()
	r.Actions = append(r.Actions, a)
	return a.ID
}
func (r *Recorder) events() {
	defer close(r.complete)
	for m := range r.conn.Events {
		switch m.Method {
		case "Runtime.executionContextCreated":
			var p struct {
				Context struct {
					ID  int64 `json:"id"`
					Aux struct {
						Frame   string `json:"frameId"`
						Default bool   `json:"isDefault"`
					} `json:"auxData"`
				} `json:"context"`
			}
			_ = json.Unmarshal(m.Params, &p)
			r.mu.Lock()
			if r.contexts[m.Session] != nil && p.Context.Aux.Default {
				r.contexts[m.Session][p.Context.ID] = p.Context.Aux.Frame
			}
			r.mu.Unlock()
			if p.Context.Aux.Default {
				go r.initializeContext(m.Session, p.Context.ID)
			}
		case "Runtime.executionContextsCleared":
			r.mu.Lock()
			r.contexts[m.Session] = map[int64]string{}
			r.mu.Unlock()
		case "Page.frameNavigated":
			var p struct {
				Frame struct{ ID, URL, ParentID string } `json:"frame"`
			}
			_ = json.Unmarshal(m.Params, &p)
			r.mu.Lock()
			r.frames[p.Frame.ID] = SanitizeURL(p.Frame.URL)
			page := r.pages[m.Session]
			r.mu.Unlock()
			if p.Frame.ParentID == "" && page != "" {
				r.mu.Lock()
				observed := len(r.Actions) > 0 && !r.manualNavigation[page]
				delete(r.manualNavigation, page)
				r.mu.Unlock()
				r.add(Action{Kind: "navigate", Page: page, Frame: p.Frame.ID, URL: SanitizeURL(p.Frame.URL), Observed: observed})
			}
		case "Runtime.bindingCalled":
			var p struct {
				Name, Payload string
				Context       int64 `json:"executionContextId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.Name != "__awTeachEvent" || len(p.Payload) > 8192 {
				continue
			}
			var a Action
			if json.Unmarshal([]byte(p.Payload), &a) != nil {
				continue
			}
			switch a.Kind {
			case "click", "fill", "select", "check", "uncheck", "press", "unsupported":
			default:
				continue
			}
			r.mu.Lock()
			page := r.pages[m.Session]
			frame := r.contexts[m.Session][p.Context]
			raw := r.frames[frame]
			paused := r.Paused
			r.mu.Unlock()
			if page == "" || paused {
				continue
			}
			a.Page = page
			a.Frame = frame
			a.URL = raw
			a.ID = 0
			a.At = time.Time{}
			a.Parameter = ""
			a.Screenshot = ""
			if len(a.Value) > 400 || len(a.Target.Selector) > 400 || len(a.Target.Name) > 400 {
				continue
			}
			frames, warning := r.framePaths(m.Session, frame)
			a.Frames = frames
			if warning != "" {
				a.Warning = warning
			}
			id := r.add(a)
			if id > 0 {
				go r.evidence(m.Session, id)
			}
		case "Target.targetCreated":
			var p struct {
				Info PageInfo `json:"targetInfo"`
			}
			_ = json.Unmarshal(m.Params, &p)
			r.mu.Lock()
			allowed := false
			for _, id := range r.pages {
				if id == p.Info.Opener {
					allowed = true
				}
			}
			r.mu.Unlock()
			r.mu.Lock()
			allowed = allowed && !r.known[p.Info.ID]
			if allowed {
				p.Info.URL = SanitizeURL(p.Info.URL)
				p.Info.Observed = !r.Paused
				r.metadata[p.Info.ID] = p.Info
			}
			r.mu.Unlock()
			if allowed && p.Info.Type == "page" {
				r.add(Action{Kind: "tab_switch", Page: p.Info.ID})
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := r.attach(ctx, p.Info.ID); err != nil {
						r.fail("Popup capture unavailable")
					}
				}()
			}
		case "Target.targetDestroyed":
			var p struct {
				ID string `json:"targetId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			r.mu.Lock()
			tracked := r.declared[p.ID]
			manual := r.manualClose[p.ID]
			for session, target := range r.pages {
				if target == p.ID {
					delete(r.pages, session)
					delete(r.contexts, session)
					delete(r.scripts, session)
				}
			}
			delete(r.manualClose, p.ID)
			r.mu.Unlock()
			if tracked {
				r.add(Action{Kind: "tab_close", Page: p.ID, Observed: !manual})
			}

		}
	}
	r.mu.Lock()
	if !r.stopped {
		r.Errors = append(r.Errors, "Browser connection interrupted; demonstration needs review")
	}
	r.mu.Unlock()
}
func (r *Recorder) fail(message string) {
	r.mu.Lock()
	r.Errors = append(r.Errors, message)
	r.mu.Unlock()
}
func (r *Recorder) evidence(session string, id int) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var safe struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if r.conn.Call(ctx, session, "Runtime.evaluate", map[string]any{"expression": "!Array.from(document.querySelectorAll('input')).some(e=>e.type==='password'||/password|passwd|otp|one.?time|verification.?code|credit.?card|card.?number|token|secret/i.test([e.name,e.id,e.autocomplete].join(' '))) && window.__awTeachVisualSafe?.() !== false && !document.querySelector('iframe,frame')", "returnByValue": true}, &safe) != nil || !safe.Result.Value {
		return
	}
	r.mu.Lock()
	if r.Paused || r.stopped || len(r.Actions) == 0 {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	var shot struct {
		Data string `json:"data"`
	}
	if r.conn.Call(ctx, session, "Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": 50}, &shot) != nil {
		return
	}
	data, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil || len(data) > 2<<20 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Paused || r.stopped || r.evidenceBytes+int64(len(data)) > 50<<20 {
		return
	}
	name := fmt.Sprintf("step-%04d.jpg", id)
	if os.WriteFile(filepath.Join(r.directory, name), data, 0660) == nil && id <= len(r.Actions) {
		r.Actions[id-1].Screenshot = name
		r.evidenceBytes += int64(len(data))
	}
}
func (r *Recorder) Control(ctx context.Context, command string) error {
	if command == "pause" {
		if err := r.Control(ctx, "flush"); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.mu.Lock()
	if command == "pause" {
		r.Paused = true
	}
	sessions := make([]string, 0, len(r.pages))
	for s := range r.pages {
		sessions = append(sessions, s)
	}
	r.mu.Unlock()
	for _, s := range sessions {
		expr := "window.__awTeachControl?.(" + fmt.Sprintf("%q", command) + ")"
		r.mu.Lock()
		ids := []int64{}
		for id := range r.contexts[s] {
			ids = append(ids, id)
		}
		r.mu.Unlock()
		for _, id := range ids {
			if err := r.conn.Call(ctx, s, "Runtime.evaluate", map[string]any{"expression": expr, "contextId": id}, nil); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			if err := r.conn.Call(ctx, s, "Runtime.evaluate", map[string]any{"expression": expr}, nil); err != nil {
				return err
			}
		}
	}
	r.mu.Lock()
	if command == "pause" {
		r.Paused = true
	}
	if command == "resume" {
		r.Paused = false
	}
	r.mu.Unlock()
	return nil
}
func (r *Recorder) Snapshot() ([]Action, []string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Action{}, r.Actions...), append([]string{}, r.Errors...), r.Paused
}
func (r *Recorder) Stop(ctx context.Context) {
	r.attachMu.Lock()
	defer r.attachMu.Unlock()
	_ = r.Control(ctx, "flush")
	time.Sleep(50 * time.Millisecond)
	_ = r.Control(ctx, "stop")
	r.mu.Lock()
	r.stopped = true
	scripts := map[string]string{}
	for s, id := range r.scripts {
		scripts[s] = id
	}
	r.mu.Unlock()
	for s, id := range scripts {
		_ = r.conn.Call(ctx, s, "Runtime.evaluate", map[string]any{"expression": "window.__awTeachControl?.('stop')"}, nil)
		_ = r.conn.Call(ctx, s, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": id}, nil)
		_ = r.conn.Call(ctx, s, "Runtime.removeBinding", map[string]any{"name": "__awTeachEvent"}, nil)
	}
	r.conn.Close()
}
func Skill(goal string, actions []Action) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Browser procedure\n\nGoal: %s\n\nStatus: draft. Sign in before running. Resolve fresh targets and stop on ambiguity.\n\n", goal)
	labels := map[string]int{}
	for _, a := range actions {
		if a.Page != "" && labels[a.Page] == 0 {
			labels[a.Page] = len(labels) + 1
		}
		fmt.Fprintf(&b, "%d. %s %s %s", a.ID, a.Kind, a.Target.Role, a.Target.Name)
		if a.Page != "" {
			fmt.Fprintf(&b, " in tab %d", labels[a.Page])
		}
		if a.Kind == "tab_open" && a.Observed {
			fmt.Fprintf(&b, " (wait for the popup opened by tab %d; do not open a duplicate)", labels[a.Opener])
		}
		if a.Kind == "tab_close" && a.Observed {
			b.WriteString(" (wait for the website to close this tab)")
		}
		if a.URL != "" {
			fmt.Fprintf(&b, " on %s", a.URL)
		}
		if a.Parameter != "" {
			fmt.Fprintf(&b, " using input `%s`", a.Parameter)
		} else if a.Value != "" {
			fmt.Fprintf(&b, " with reviewed value %q", a.Value)
		}
		if len(a.Frames) > 0 {
			fmt.Fprintf(&b, " in frame %q", a.Frames)
		}
		fmt.Fprintln(&b)
	}
	b.WriteString("\nValidate the user-reviewed expected outcome before reporting success.\n")
	return b.String()
}

type frameTree struct {
	Frame struct {
		ID string `json:"id"`
	} `json:"frame"`
	Children []frameTree `json:"childFrames"`
}

func (r *Recorder) framePaths(session, frame string) ([]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var tree struct {
		Tree frameTree `json:"frameTree"`
	}
	if r.conn.Call(ctx, session, "Page.getFrameTree", map[string]any{}, &tree) != nil {
		return nil, "Cannot verify frame ownership"
	}
	if tree.Tree.Frame.ID == frame {
		return nil, ""
	}
	var find func(frameTree) ([]string, bool)
	find = func(node frameTree) ([]string, bool) {
		if node.Frame.ID == frame {
			return []string{frame}, true
		}
		for _, child := range node.Children {
			if ids, ok := find(child); ok {
				return append([]string{node.Frame.ID}, ids...), true
			}
		}
		return nil, false
	}
	ids, ok := find(tree.Tree)
	if !ok {
		return nil, "Frame is not in the recorded page"
	}
	paths := []string{}
	for _, id := range ids[1:] {
		var owner struct {
			Backend int `json:"backendNodeId"`
		}
		if r.conn.Call(ctx, session, "DOM.getFrameOwner", map[string]any{"frameId": id}, &owner) != nil {
			return nil, "Unsupported frame capture"
		}
		var node struct {
			Node struct {
				Attributes []string `json:"attributes"`
			} `json:"node"`
		}
		if r.conn.Call(ctx, session, "DOM.describeNode", map[string]any{"backendNodeId": owner.Backend}, &node) != nil {
			return nil, "Cannot inspect frame locator"
		}
		attrs := map[string]string{}
		for i := 0; i+1 < len(node.Node.Attributes); i += 2 {
			attrs[node.Node.Attributes[i]] = node.Node.Attributes[i+1]
		}
		selector := ""
		for _, attr := range []string{"data-testid", "id", "name"} {
			if value := attrs[attr]; value != "" && len(value) <= 200 {
				raw, _ := json.Marshal(value)
				selector = "iframe[" + attr + "=" + string(raw) + "]"
				break
			}
		}
		if selector == "" {
			return nil, "Frame needs a stable id, name or test attribute"
		}
		paths = append(paths, selector)
	}
	return paths, ""
}

func (r *Recorder) initializeContext(session string, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if r.conn.Call(ctx, session, "Runtime.evaluate", map[string]any{"expression": script, "contextId": id}, nil) != nil {
		return
	}
	r.mu.Lock()
	command := "resume"
	if r.Paused || r.stopped {
		command = "pause"
	}
	r.mu.Unlock()
	_ = r.conn.Call(ctx, session, "Runtime.evaluate", map[string]any{"expression": "window.__awTeachControl?.(" + fmt.Sprintf("%q", command) + ")", "contextId": id}, nil)
}
