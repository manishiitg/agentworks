package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/browserteach"
)

type teachTab struct {
	Ref    string `json:"tabId"`
	Target string `json:"targetId"`
	Active bool   `json:"active"`
}

func teachingTabs(run func(...string) ([]byte, error)) ([]teachTab, error) {
	data, err := run("tab")
	if err != nil {
		return nil, err
	}
	var result struct {
		Data struct {
			Tabs []teachTab `json:"tabs"`
		} `json:"data"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data.Tabs, nil
}
func activeTeachingTarget(run func(...string) ([]byte, error)) (string, error) {
	tabs, err := teachingTabs(run)
	if err != nil {
		return "", err
	}
	for _, tab := range tabs {
		if tab.Active && tab.Target != "" {
			return tab.Target, nil
		}
	}
	return "", fmt.Errorf("Cannot identify the active teaching tab")
}

// Recorded target IDs are logical keys, never live commands. Every test binds
// those keys to fresh targets and switches only to a target in that mapping.
type teachingReplayTabs struct {
	ctx     context.Context
	run     func(...string) ([]byte, error)
	conn    *browserteach.Connection
	targets map[string]string
	known   map[string]bool
}

func validateTeachingTabs(actions []browserteach.Action) error {
	pages := map[string]bool{}
	root := ""
	for _, action := range actions {
		if action.Page == "" {
			return fmt.Errorf("Missing recorded tab identity")
		}
		if root == "" {
			if action.Kind == "tab_open" || action.Kind == "tab_close" {
				return fmt.Errorf("Missing starting tab")
			}
			root = action.Page
			pages[root] = true
		}
		if action.Kind == "tab_open" {
			if pages[action.Page] {
				return fmt.Errorf("Duplicate recorded tab identity")
			}
			if action.Observed && !pages[action.Opener] {
				return fmt.Errorf("Popup has no recorded opener")
			}
			pages[action.Page] = true
		} else if !pages[action.Page] {
			return fmt.Errorf("Recorded tab is missing or already closed")
		}
		if action.Kind == "tab_close" {
			delete(pages, action.Page)
		}
	}
	return nil
}
func newTeachingReplayTabs(ctx context.Context, run func(...string) ([]byte, error), actions []browserteach.Action) (*teachingReplayTabs, error) {
	if err := validateTeachingTabs(actions); err != nil {
		return nil, err
	}
	target, err := activeTeachingTarget(run)
	if err != nil {
		return nil, err
	}
	out, err := run("get", "cdp-url")
	if err != nil {
		return nil, err
	}
	var result struct {
		Data struct {
			URL string `json:"cdpUrl"`
		} `json:"data"`
	}
	if err = json.Unmarshal(out, &result); err != nil {
		return nil, err
	}
	conn, err := browserteach.Connect(ctx, result.Data.URL)
	if err != nil {
		return nil, err
	}
	pages, err := conn.Pages(ctx)
	if err != nil {
		conn.Close()
		return nil, err
	}
	t := &teachingReplayTabs{ctx: ctx, run: run, conn: conn, targets: map[string]string{actions[0].Page: target}, known: map[string]bool{}}
	for _, page := range pages {
		t.known[page.ID] = true
	}
	return t, nil
}
func (t *teachingReplayTabs) selectPage(page string) error {
	target := t.targets[page]
	if target == "" {
		return fmt.Errorf("Recorded tab is unavailable")
	}
	tabs, err := teachingTabs(t.run)
	if err != nil {
		return err
	}
	for _, tab := range tabs {
		if tab.Target == target {
			if tab.Active {
				return nil
			}
			_, err = t.run("tab", tab.Ref)
			return err
		}
	}
	return fmt.Errorf("Recorded tab closed unexpectedly")
}
func (t *teachingReplayTabs) apply(action browserteach.Action) (bool, error) {
	switch action.Kind {
	case "tab_open":
		if action.Observed {
			opener := t.targets[action.Opener]
			for i := 0; i < 40; i++ {
				pages, err := t.conn.Pages(t.ctx)
				if err != nil {
					return true, err
				}
				candidates := []string{}
				for _, page := range pages {
					if page.Type == "page" && page.Opener == opener && !t.known[page.ID] {
						candidates = append(candidates, page.ID)
					}
				}
				if len(candidates) > 1 {
					return true, fmt.Errorf("Popup is ambiguous; adjust the task")
				}
				if len(candidates) == 1 {
					t.targets[action.Page] = candidates[0]
					t.known[candidates[0]] = true
					return true, t.selectPage(action.Page)
				}
				select {
				case <-t.ctx.Done():
					return true, t.ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
			}
			return true, fmt.Errorf("Expected popup did not open")
		}
		address := action.URL
		if address == "" {
			address = "about:blank"
		}
		if !validTeachingAddress(address) {
			return true, fmt.Errorf("Unsupported new-tab address")
		}
		if _, err := t.run("tab", "new", address); err != nil {
			return true, err
		}
		target, err := activeTeachingTarget(t.run)
		if err != nil {
			return true, err
		}
		if t.known[target] {
			return true, fmt.Errorf("Browser did not create a fresh tab")
		}
		t.targets[action.Page] = target
		t.known[target] = true
		return true, nil
	case "tab_close":
		target := t.targets[action.Page]
		if action.Observed {
			for i := 0; i < 40; i++ {
				pages, err := t.conn.Pages(t.ctx)
				if err != nil {
					return true, err
				}
				found := false
				for _, page := range pages {
					if page.ID == target {
						found = true
					}
				}
				if !found {
					delete(t.targets, action.Page)
					return true, nil
				}
				select {
				case <-t.ctx.Done():
					return true, t.ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
			}
			return true, fmt.Errorf("Expected tab did not close")
		}
		tabs, err := teachingTabs(t.run)
		if err != nil {
			return true, err
		}
		for _, tab := range tabs {
			if tab.Target == target {
				if len(tabs) < 2 {
					return true, fmt.Errorf("Cannot close the only browser tab")
				}
				_, err = t.run("tab", "close", tab.Ref)
				delete(t.targets, action.Page)
				return true, err
			}
		}
		return true, fmt.Errorf("Recorded tab is unavailable")
	case "tab_switch":
		return true, t.selectPage(action.Page)
	default:
		return false, t.selectPage(action.Page)
	}
}
func validTeachingAddress(address string) bool {
	if address == "about:blank" {
		return true
	}
	u, err := url.Parse(address)
	return err == nil && u.User == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}
