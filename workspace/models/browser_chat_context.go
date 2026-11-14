package models

// BrowserChatContextRequest is a fixed read operation on an already-admitted
// live browser. No caller script, selector, URL or output path is accepted.
type BrowserChatContextRequest struct {
	DocumentToken string   `json:"document_token,omitempty"`
	X             *float64 `json:"x,omitempty"`
	Y             *float64 `json:"y,omitempty"`
}

type BrowserContextRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type BrowserChatContext struct {
	BrowserSession string             `json:"browser_session,omitempty"`
	DocumentToken  string             `json:"document_token"`
	URL            string             `json:"url"`
	Width          float64            `json:"width"`
	Height         float64            `json:"height"`
	Selector       string             `json:"selector,omitempty"`
	Text           string             `json:"text,omitempty"`
	Rect           BrowserContextRect `json:"rect"`
	ImageBase64    string             `json:"image_base64,omitempty"`
}
