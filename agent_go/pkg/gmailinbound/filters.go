package gmailinbound

import (
	"fmt"
	"strings"
)

// Filters only narrow an already authenticated, owner-authorized delivery.
// Every specified keyword and condition must match. Nil means no filters.
type Filters struct {
	SubjectContains []string `json:"subject_contains,omitempty"`
	BodyContains    []string `json:"body_contains,omitempty"`
	HasAttachments  *bool    `json:"has_attachments,omitempty"`
	NewThreadsOnly  bool     `json:"new_threads_only,omitempty"`
}

func NormalizeFilters(f *Filters) (*Filters, error) {
	if f == nil {
		return nil, nil
	}
	out := *f
	normalize := func(field string, terms []string) ([]string, error) {
		if len(terms) > 10 {
			return nil, fmt.Errorf("%s accepts at most 10 keywords", field)
		}
		result := []string{}
		seen := map[string]bool{}
		for _, term := range terms {
			term = strings.TrimSpace(term)
			if term == "" || len(term) > 256 {
				return nil, fmt.Errorf("%s keywords must contain 1–256 bytes after trimming", field)
			}
			key := strings.ToLower(term)
			if !seen[key] {
				result = append(result, term)
				seen[key] = true
			}
		}
		return result, nil
	}
	var err error
	if out.SubjectContains, err = normalize("subject_contains", f.SubjectContains); err != nil {
		return nil, err
	}
	if out.BodyContains, err = normalize("body_contains", f.BodyContains); err != nil {
		return nil, err
	}
	if len(out.SubjectContains) == 0 && len(out.BodyContains) == 0 && out.HasAttachments == nil && !out.NewThreadsOnly {
		return nil, nil
	}
	return &out, nil
}

// Mismatch returns a human-readable reason, without putting email content in it.
// Thread history is checked atomically by Store, after these content checks.
func (f *Filters) Mismatch(m Message) string {
	if f == nil {
		return ""
	}
	for _, term := range f.SubjectContains {
		if !strings.Contains(strings.ToLower(m.Subject), strings.ToLower(term)) {
			return "Subject does not match the required keywords"
		}
	}
	for _, term := range f.BodyContains {
		if !strings.Contains(strings.ToLower(m.Body), strings.ToLower(term)) {
			return "Body does not match the required keywords"
		}
	}
	if f.HasAttachments != nil && *f.HasAttachments != (len(m.Attachments) > 0) {
		return "Attachment presence does not match the filter"
	}
	if f.NewThreadsOnly && (m.IsReply || m.ThreadID == "") {
		return "New threads only: replies or messages without a thread ID are excluded"
	}
	return ""
}
