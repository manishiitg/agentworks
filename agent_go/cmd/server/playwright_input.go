package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
)

// Visibility includes Run users; sending input uses the existing Builder rule.
func (api *StreamingAPI) canControlPlaywright(claims *UserClaims, workspace string, session *playwrightLiveSession) bool {
	if !api.playwrightVisible(claims, workspace, session.owner, session.workspace) {
		return false
	}
	return api.canControlLiveBrowser(context.Background(), claims, workspace)
}

// Only browser UI operations cross to the test producer. The viewer cannot
// choose a protocol method, execute JavaScript, or change the producer's scope.
func playwrightViewerCommand(raw []byte) ([]byte, error) {
	var m struct {
		Type                                         string `json:"type"`
		EventType                                    string `json:"eventType"`
		X, Y, DeltaX, DeltaY                         float64
		Button                                       string `json:"button"`
		ClickCount, Modifiers, WindowsVirtualKeyCode int
		Key, Code, Text, Tab, URL, Action, RequestID string
		Width, Height                                int
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	clean := map[string]any{"type": m.Type}
	validNumber := func(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n) <= 32768 }
	validURL := func(s string) bool {
		if s == "about:blank" {
			return true
		}
		u, err := url.Parse(s)
		return err == nil && len(s) <= 2048 && u.User == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
	}
	bad := func() ([]byte, error) { return nil, fmt.Errorf("unsupported browser input") }
	switch m.Type {
	case "input_mouse":
		if m.EventType != "mousePressed" && m.EventType != "mouseReleased" && m.EventType != "mouseMoved" && m.EventType != "mouseWheel" {
			return bad()
		}
		if !validNumber(m.X) || !validNumber(m.Y) || !validNumber(m.DeltaX) || !validNumber(m.DeltaY) || m.ClickCount < 0 || m.ClickCount > 3 {
			return bad()
		}
		if m.Button != "" && m.Button != "left" && m.Button != "middle" && m.Button != "right" && m.Button != "none" {
			return bad()
		}
		clean["eventType"], clean["x"], clean["y"] = m.EventType, m.X, m.Y
		clean["button"], clean["clickCount"] = m.Button, m.ClickCount
		clean["deltaX"], clean["deltaY"] = m.DeltaX, m.DeltaY
	case "input_keyboard":
		if m.EventType != "keyDown" && m.EventType != "keyUp" {
			return bad()
		}
		if len(m.Key) > 80 || len(m.Code) > 80 || len(m.Text) > 128 || m.Modifiers < 0 || m.Modifiers > 15 || m.WindowsVirtualKeyCode < 0 || m.WindowsVirtualKeyCode > 65535 {
			return bad()
		}
		clean["eventType"], clean["key"], clean["code"], clean["text"] = m.EventType, m.Key, m.Code, m.Text
		clean["modifiers"], clean["windowsVirtualKeyCode"] = m.Modifiers, m.WindowsVirtualKeyCode
	case "input_text":
		if len(m.Text) == 0 || len(m.Text) > 8192 {
			return bad()
		}
		clean["text"] = m.Text
	case "clipboard_copy":
		if !liveBrowserClipboardRequest.MatchString(m.RequestID) {
			return bad()
		}
		clean["requestId"] = m.RequestID
	case "navigate", "new_tab":
		if !validURL(m.URL) {
			return bad()
		}
		clean["url"] = m.URL
	case "history":
		if m.Action != "back" && m.Action != "forward" && m.Action != "reload" {
			return bad()
		}
		clean["action"] = m.Action
	case "switch_tab", "close_tab":
		if !liveBrowserTabRef.MatchString(m.Tab) {
			return bad()
		}
		clean["tab"] = m.Tab
	case "resize_viewport":
		if m.Width < 320 || m.Width > 1920 || m.Height < 320 || m.Height > 1920 {
			return bad()
		}
		clean["width"], clean["height"] = m.Width, m.Height
	default:
		return bad()
	}
	return json.Marshal(clean)
}

func playwrightViewerReply(raw []byte) ([]byte, error) {
	var message struct{ Type, RequestID, Text string }
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	if message.Type == "viewer_error" {
		return json.Marshal(map[string]string{"type": "viewer_error", "message": "Unable to perform that browser action. The script continued."})
	}
	if message.Type != "clipboard" || !liveBrowserClipboardRequest.MatchString(message.RequestID) || len(message.Text) > 8192 {
		return nil, fmt.Errorf("invalid browser reply")
	}
	return json.Marshal(map[string]string{"type": "clipboard", "requestId": message.RequestID, "text": message.Text})
}
