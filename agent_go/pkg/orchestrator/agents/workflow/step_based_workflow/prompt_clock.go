package step_based_workflow

import "time"

// promptClock gives agents the current time in UTC, with the server's local
// time alongside when it differs (PLAT-635). Step prompts used to get bare
// local time, so workflows taught every step to convert IST to UTC.
func promptClock(now time.Time) (date, clock string) {
	utc := now.UTC()
	date, clock = utc.Format("2006-01-02"), utc.Format("15:04:05")+" UTC"
	if name, offset := now.Zone(); offset != 0 {
		clock += " (server local " + now.Format("2006-01-02 15:04") + " " + name + ")"
	}
	return date, clock
}

func promptDate(now time.Time) string { d, _ := promptClock(now); return d }

func promptTime(now time.Time) string { _, c := promptClock(now); return c }
