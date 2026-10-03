package browserteach

import "context"

// PageInfo comes only from the trusted private browser connection.
type PageInfo struct {
	ID       string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Opener   string `json:"openerId"`
	Observed bool   `json:"-"`
}

func (c *Connection) Pages(ctx context.Context) ([]PageInfo, error) {
	var result struct {
		Pages []PageInfo `json:"targetInfos"`
	}
	err := c.Call(ctx, "", "Target.getTargets", map[string]any{}, &result)
	return result.Pages, err
}

// Select attaches only to a tab deliberately selected by the viewer. Existing
// unrelated Chrome tabs are never swept into the recording.
func (r *Recorder) Select(ctx context.Context, target string) error {
	if err := r.attach(ctx, target); err != nil {
		r.fail("Selected tab capture unavailable; demonstration needs review")
		return err
	}
	r.add(Action{Kind: "tab_switch", Page: target})
	return nil
}
func (r *Recorder) PrepareClose(target string) {
	r.mu.Lock()
	r.manualClose[target] = true
	r.mu.Unlock()
}

// Viewer address-bar navigation must replay as an explicit open, rather than
// wait for a link-triggered navigation that never occurs.
func (r *Recorder) PrepareNavigation(target string, pending bool) {
	r.mu.Lock()
	r.manualNavigation[target] = pending
	r.mu.Unlock()
}
