package virtualtools

import "testing"

func TestRequiredNotificationEmailSubject(t *testing.T) {
	for _, subject := range []interface{}{nil, "", "  ", 42, "one\ntwo", "=?UTF-8?q?test?="} {
		if validateNotificationEmailSubject(map[string]interface{}{"email_subject": subject}, true, nil) == nil {
			t.Fatalf("accepted %v", subject)
		}
	}
	args := map[string]interface{}{"summary_title": "Not a subject"}
	if validateNotificationEmailSubject(args, true, nil) == nil {
		t.Fatal("summary title substituted")
	}
	if validateNotificationEmailSubject(args, false, nil) != nil {
		t.Fatal("Slack-only requires subject")
	}
	if validateNotificationEmailSubject(args, true, []string{"gmail"}) != nil {
		t.Fatal("excluded Gmail requires subject")
	}
	if validateNotificationEmailSubject(map[string]interface{}{"email_subject": "project-a — daily update"}, true, nil) != nil {
		t.Fatal("valid Unicode subject rejected")
	}
}
