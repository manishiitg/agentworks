package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var liveBrowserClipboardRequest = regexp.MustCompile(`^copy-[0-9]{1,12}$`)

// Only a fixed selection reader is exposed to the viewer. Never use the
// server/Chrome OS clipboard: it can be shared by unrelated browser scopes.
const liveBrowserSelectionScript = `(() => {
  const read = root => {
    const element = root.activeElement;
    if (element?.shadowRoot) return read(element.shadowRoot);
    if (element?.tagName === 'IFRAME' || element?.tagName === 'FRAME') {
      try { if (element.contentDocument) return read(element.contentDocument); } catch {}
      throw new Error('Select text in the main page before copying');
    }
    if (element && typeof element.selectionStart === 'number' && typeof element.selectionEnd === 'number')
      return element.value.slice(element.selectionStart, element.selectionEnd);
    return (root.getSelection?.() || root.ownerDocument?.getSelection?.())?.toString() || '';
  };
  const text = read(document);
  if (text.length > 65536) throw new Error('Selection is too large');
  return text;
})()`

func liveBrowserSelection(output string) (string, error) {
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Result *string `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(output), &response) != nil || !response.Success || response.Data.Result == nil || len(*response.Data.Result) > 256*1024 {
		return "", fmt.Errorf("unable to read browser selection")
	}
	return *response.Data.Result, nil
}

func forwardBrowserViewerText(ctx context.Context, workspaceURL, session, userID, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"text": text})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(workspaceURL, "/")+"/api/browser/live/"+url.PathEscape(session)+"/text", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Token", strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")))
	request.Header.Set("X-User-ID", userID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("browser text insertion failed")
	}
	return nil
}
