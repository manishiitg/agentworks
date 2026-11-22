package costledger

import (
	"sync/atomic"
	"time"
)

// dayLocation is where a calendar day starts for daily totals and "today". One place for every cost view, so the
// Costs page's days and a chat's "today" agree (they used UTC and local midnight before, and showed different
// amounts for the same day).
var dayLocation atomic.Pointer[time.Location]

// SetDayLocation sets the time zone of cost days (the server's display time zone). nil returns to the machine's own.
func SetDayLocation(loc *time.Location) { dayLocation.Store(loc) }

// DayLocation is the time zone cost days use: the display time zone when set, otherwise the machine's local time.
func DayLocation() *time.Location {
	if loc := dayLocation.Load(); loc != nil {
		return loc
	}
	return time.Local
}

// Day is the YYYY-MM-DD cost day of a moment.
func Day(ts time.Time) string { return ts.In(DayLocation()).Format("2006-01-02") }
