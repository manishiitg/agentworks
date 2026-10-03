package gmailinbound

import (
	"fmt"
	"net/mail"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Content conditions combine with AND; alternatives within an *_any list or
// sender allowlist combine with OR. Without senders, authorization stays owner-only.
type Filters struct {
	SenderAllowlist    []string `json:"sender_allowlist,omitempty"`
	SubjectContains    []string `json:"subject_contains,omitempty"`
	BodyContains       []string `json:"body_contains,omitempty"`
	SubjectContainsAny []string `json:"subject_contains_any,omitempty"`
	BodyContainsAny    []string `json:"body_contains_any,omitempty"`
	AllowAutomatic     bool     `json:"allow_automatic,omitempty"`
	HasAttachments     *bool    `json:"has_attachments,omitempty"`
	NewThreadsOnly     bool     `json:"new_threads_only,omitempty"`
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
	if out.SubjectContainsAny, err = normalize("subject_contains_any", f.SubjectContainsAny); err != nil {
		return nil, err
	}
	if out.BodyContainsAny, err = normalize("body_contains_any", f.BodyContainsAny); err != nil {
		return nil, err
	}
	if out.SenderAllowlist, err = normalize("sender_allowlist", f.SenderAllowlist); err != nil {
		return nil, err
	}
	for i, sender := range out.SenderAllowlist {
		sender = strings.ToLower(sender)
		domain := strings.TrimPrefix(sender, "@")
		if !strings.HasPrefix(sender, "@") {
			address, e := mail.ParseAddress(sender)
			if e != nil || address.Address != sender || strings.ContainsAny(sender, "*<>\r\n") {
				return nil, fmt.Errorf("sender_allowlist accepts exact email addresses or @domain names")
			}
			_, domain, _ = strings.Cut(sender, "@")
		}
		if !validSenderDomain(domain) {
			return nil, fmt.Errorf("sender_allowlist contains an invalid domain")
		}
		if strings.HasPrefix(sender, "@") && broadMailboxDomain(domain) {
			return nil, fmt.Errorf("whole public mailbox domains such as @gmail.com are not allowed; list exact email addresses instead")
		}
		out.SenderAllowlist[i] = sender
	}
	if out.AllowAutomatic && len(out.SenderAllowlist) == 0 {
		return nil, fmt.Errorf("allow_automatic requires an explicit sender_allowlist")
	}
	if len(out.SenderAllowlist) == 0 && len(out.SubjectContains) == 0 && len(out.BodyContains) == 0 && len(out.SubjectContainsAny) == 0 && len(out.BodyContainsAny) == 0 && out.HasAttachments == nil && !out.NewThreadsOnly {
		return nil, nil
	}
	return &out, nil
}

func broadMailboxDomain(domain string) bool {
	suffix, _ := publicsuffix.PublicSuffix(domain)
	if suffix == domain {
		return true
	}
	// These common shared mailbox domains are never an organization allowlist.
	// Unknown domains still require explicit, configuration-bound owner consent.
	switch domain {
	case "gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "hotmail.co.uk", "live.com", "msn.com", "yahoo.com", "yahoo.co.uk", "ymail.com", "rocketmail.com", "icloud.com", "me.com", "mac.com", "aol.com", "proton.me", "protonmail.com", "pm.me", "gmx.com", "gmx.net", "mail.com", "yandex.com", "yandex.ru", "zoho.com", "fastmail.com", "hey.com", "tutanota.com", "tuta.com":
		return true
	}
	return false
}

func validSenderDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(domain) > 253 || len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Domain entries match the exact From domain, never suffixes or display names.
func (f *Filters) SenderAllowed(from string) bool {
	if f == nil {
		return false
	}
	address, err := mail.ParseAddress(from)
	if err != nil || address.Address != from {
		return false
	}
	from = strings.ToLower(from)
	_, domain, _ := strings.Cut(from, "@")
	for _, allowed := range f.SenderAllowlist {
		if strings.EqualFold(allowed, from) || strings.HasPrefix(allowed, "@") && strings.EqualFold(allowed[1:], domain) {
			return true
		}
	}
	return false
}

// Explicitly selected notification senders may generate automated mail. Spam,
// trash, bounces and automatic replies are always excluded to prevent loops.
func (f *Filters) AcceptsMessageKind(m Message) bool {
	return !m.Blocked && (!m.Automatic || f != nil && f.AllowAutomatic && f.SenderAllowed(m.From))
}

// Mismatch returns a human-readable reason, without putting email content in it.
// Thread history is checked atomically by Store, after these content checks.
func (f *Filters) Mismatch(m Message) string {
	if f == nil {
		return ""
	}
	if len(f.SenderAllowlist) > 0 && !f.SenderAllowed(m.From) {
		return "Sender does not match the allowed addresses or domains"
	}
	containsAny := func(text string, terms []string) bool {
		if len(terms) == 0 {
			return true
		}
		for _, term := range terms {
			if strings.Contains(strings.ToLower(text), strings.ToLower(term)) {
				return true
			}
		}
		return false
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
	if !containsAny(m.Subject, f.SubjectContainsAny) {
		return "Subject does not match any of the alternative keywords"
	}
	if !containsAny(m.Body, f.BodyContainsAny) {
		return "Body does not match any of the alternative keywords"
	}
	if f.HasAttachments != nil && *f.HasAttachments != (len(m.Attachments) > 0) {
		return "Attachment presence does not match the filter"
	}
	if f.NewThreadsOnly && (m.IsReply || m.ThreadID == "") {
		return "New threads only: replies or messages without a thread ID are excluded"
	}
	return ""
}
