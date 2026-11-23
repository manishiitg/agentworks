package musecli

import "time"

// Missing journals are normal for restored/ended sessions. Do not walk every
// dated session directory on each UI poll. A new account/session retries now.
type museLogPathRetry struct {
	identity string
	next     time.Time
	delay    time.Duration
}

func (r *museLogPathRetry) resolve(identity string, lookup func() string) string {
	return r.resolveAt(time.Now(), identity, lookup)
}
func (r *museLogPathRetry) resolveAt(now time.Time, identity string, lookup func() string) string {
	if r.identity != identity {
		*r = museLogPathRetry{identity: identity}
	}
	if now.Before(r.next) {
		return ""
	}
	path := lookup()
	if path != "" {
		r.next = time.Time{}
		r.delay = 0
		return path
	}
	if r.delay == 0 {
		r.delay = time.Second
	} else {
		r.delay *= 2
	}
	if r.delay > 30*time.Second {
		r.delay = 30 * time.Second
	}
	r.next = now.Add(r.delay)
	return ""
}
