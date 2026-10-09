package virtualtools

import "testing"

func TestGmailContentUsesExplicitHTMLOnly(t *testing.T) {
	// The retired email_body argument is ignored even when an old caller sends
	// HTML through it. There is no hidden compatibility path.
	gc, err := gmailContentFromArgs(map[string]interface{}{
		"email_body": "<html><body><h2>Hi</h2><p>hello</p></body></html>",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gc != nil {
		t.Fatalf("retired email_body unexpectedly created Gmail content: %+v", gc)
	}

	// Explicit email_html is the only inline rich-body argument.
	gc2, err := gmailContentFromArgs(map[string]interface{}{
		"email_subject": "Test",
		"email_html":    "<h1>Designed</h1>",
	})
	if err != nil || gc2 == nil || gc2.HTMLBody != "<h1>Designed</h1>" {
		t.Fatalf("explicit HTML content wrong: gc=%+v err=%v", gc2, err)
	}
}
