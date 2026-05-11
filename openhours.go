package openhours

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Layout holds a pre-parsed opening-hours schedule. The zero value is valid
// but will never match. Use Parse to obtain a Layout from a string.
type Layout struct{ ivs []weekInterval }

// ErrInvalidLayout is returned by Parse when the layout string contains no
// recognisable schedule entries.
var ErrInvalidLayout = errors.New("openhours: invalid layout")

// Parse compiles a layout string into a Layout for repeated evaluation.
// Unlike the package-level Match, Parse does not use the global cache;
// the caller owns the returned Layout value.
func Parse(layout string) (Layout, error) {
	ivs := parse(layout)
	if len(ivs) == 0 {
		return Layout{}, ErrInvalidLayout
	}

	return Layout{ivs: ivs}, nil
}

// Match reports whether t falls within the open hours described by l.
func (l Layout) Match(t time.Time) bool {
	return matchWeek(l.ivs, weekMinutes(t))
}

// Split returns a sorted slice of open/close time boundaries anchored to the
// week containing t, together with a flag reporting whether t itself is open.
func (l Layout) Split(t time.Time) ([]time.Time, bool) {
	monday := mondayOf(t)
	out := make([]time.Time, 0, len(l.ivs)*2)

	for _, iv := range l.ivs {
		out = append(out, minsToTime(monday, iv.open), closeToTime(monday, iv.close))
	}

	return out, matchWeek(l.ivs, weekMinutes(t))
}

// Format returns a human-readable summary of the schedule for the week
// containing t, marking the currently-open interval with '*'.
func (l Layout) Format(t time.Time) string {
	if len(l.ivs) == 0 {
		return ""
	}

	monday := mondayOf(t)
	tm := weekMinutes(t)

	var (
		lastDay int
		sb      strings.Builder
	)

	for _, iv := range l.ivs {
		openT := minsToTime(monday, iv.open)
		closeT := closeToTime(monday, iv.close)

		d := openT.Day()

		if d != lastDay {
			if lastDay != 0 {
				sb.WriteByte('\n')
			}

			sb.WriteString(openT.Format("Mon, 02 Jan 15:04"))

			lastDay = d
		} else {
			sb.WriteByte(' ')
			sb.WriteString(openT.Format("15:04"))
		}

		if isOpenAt(iv, tm) {
			sb.WriteByte('*')
		} else {
			sb.WriteByte('-')
		}

		sb.WriteString(closeT.Format("15:04"))
	}

	return sb.String()
}

// Until returns the nearest open/close boundary after t.
// If t falls within an open interval, open is true and boundary is when it closes.
// If t falls in a gap, open is false and boundary is when it next opens.
// boundary is the zero Time if the schedule has no intervals.
// Note: boundary is the exact transition instant — a midnight close is 00:00, not 23:59.
func (l Layout) Until(t time.Time) (open bool, boundary time.Time) {
	if len(l.ivs) == 0 {
		return false, time.Time{}
	}

	tm := weekMinutes(t)
	monday := mondayOf(t)

	for _, iv := range l.ivs {
		if iv.open <= tm && tm < iv.close {
			return true, minsToTime(monday, iv.close)
		}
		// Overnight Sunday→Monday: close exceeds minsPerWeek; fold back to this week's Monday.
		if iv.close > minsPerWeek && iv.open <= tm+minsPerWeek && tm+minsPerWeek < iv.close {
			return true, minsToTime(monday, iv.close-minsPerWeek)
		}
	}

	// Closed: first interval that opens later this week.
	for _, iv := range l.ivs {
		if iv.open > tm {
			return false, minsToTime(monday, iv.open)
		}
	}

	// Nothing opens later this week; wrap to next week's first interval.
	return false, minsToTime(monday, l.ivs[0].open+minsPerWeek)
}

// Match reports whether t falls within the open hours described by layout.
// An unparsable layout yields false — no error is returned.
// Results are cached in a package-level cache; see SetCacheSize.
func Match(layout string, t time.Time) bool {
	initCache()

	return getOrParse(layout).Match(t)
}

// --- implementation ---

const (
	defaultCacheSize = 4096

	minsPerHour = 60
	minsPerDay  = 24 * minsPerHour // 1440
	minsPerWeek = 7 * minsPerDay   // 10080
	daysPerWeek = 7
)

// weekInterval stores an open/close pair as minutes from Monday 00:00.
// close may exceed weekMins for Sunday→Monday overnight intervals.
type weekInterval struct {
	open  int
	close int
}

// clockEntry is a single slot in the second-chance ring buffer.
type clockEntry struct {
	key   string
	value Layout
	ref   atomic.Bool // referenced since last clock-hand pass
}

//nolint:gochecknoglobals
var (
	cacheSize atomic.Int32

	cacheMu  sync.RWMutex
	cache    map[string]int // layout → ring index
	ring     []clockEntry
	clockPos int

	initOnce sync.Once
)

func init() { //nolint: gochecknoinits
	cacheSize.Store(defaultCacheSize)
}

// SetCacheSize sets the maximum number of distinct layout strings held in the
// package-level parse cache. It must be called before the first call to Match,
// Split, or NewSplitter; later calls have no effect once the cache is initialised.
func SetCacheSize(n int) {
	if n < 1 {
		n = defaultCacheSize
	}

	cacheSize.Store(int32(n))
}

// initCache pre-allocates the ring buffer on first use. It reads cacheSize
// at init time; later calls to SetCacheSize have no effect.
func initCache() {
	initOnce.Do(func() {
		n := int(cacheSize.Load())
		cache = make(map[string]int, n)
		ring = make([]clockEntry, n)
	})
}

func getOrParse(layout string) Layout {
	if layout == "24/7" {
		layout = "Mo-Su"
	}

	cacheMu.RLock()

	idx, ok := cache[layout]
	if ok {
		ring[idx].ref.Store(true)
		l := ring[idx].value

		cacheMu.RUnlock()

		return l
	}

	cacheMu.RUnlock()

	parsed := Layout{ivs: parse(layout)}

	cacheMu.Lock()

	// Double-check: another goroutine may have raced us.
	idx, ok = cache[layout]
	if ok {
		ring[idx].ref.Store(true)
		l := ring[idx].value

		cacheMu.Unlock()

		return l
	}

	// Clock eviction: advance hand past recently-referenced entries.
	for ring[clockPos].ref.Load() {
		ring[clockPos].ref.Store(false)
		clockPos = (clockPos + 1) % len(ring)
	}

	// Evict stale entry and insert the new one.
	old := ring[clockPos].key
	if old != "" {
		delete(cache, old)
	}

	ring[clockPos].key = layout
	ring[clockPos].value = parsed
	ring[clockPos].ref.Store(true)
	cache[layout] = clockPos

	clockPos = (clockPos + 1) % len(ring)

	cacheMu.Unlock()

	return parsed
}

func parse(layout string) []weekInterval { //nolint:gocognit,gocyclo,cyclop
	if layout == "24/7" {
		layout = "Mo-Su"
	}

	var (
		bufDay  = make([]int, 0, daysPerWeek)
		bufHour = make([]rune, 0, 2)
		bufMin  = make([]rune, 0, 2)

		wasSpan, wasDump bool
		prevOpenMinOfDay int

		ivs []weekInterval
	)

	flushOpen := func(h, m int) {
		prevOpenMinOfDay = h*minsPerHour + m
	}

	flushClose := func(hc, mc int) {
		const h24 = 24

		// 00:00 and 24:00 as close time mean "end of day": store as dayMins (exclusive)
		// so that the last minute (23:59) is correctly included in the open window.
		var closeMinOfDay int

		if (hc == 0 && mc == 0) || hc == h24 {
			closeMinOfDay = minsPerDay
		} else {
			closeMinOfDay = hc*minsPerHour + mc
		}

		isOvernight := closeMinOfDay < prevOpenMinOfDay

		for _, day := range bufDay {
			base := (day - 1) * minsPerDay
			openMin := base + prevOpenMinOfDay

			closeBase := base

			if isOvernight {
				closeBase = (day % daysPerWeek) * minsPerDay
				if closeBase < base {
					closeBase += minsPerWeek
				}
			}

			ivs = append(ivs, weekInterval{open: openMin, close: closeBase + closeMinOfDay})
		}
	}

	appendDigit := func(r rune) bool {
		if len(bufHour) < 2 {
			bufHour = append(bufHour, r)

			return false
		}

		bufMin = append(bufMin, r)

		return len(bufMin) == 2
	}

	for i, r := range layout {
		if '0' <= r && r <= '9' {
			if !appendDigit(r) {
				continue
			}

			h, m := rtoi(bufHour), rtoi(bufMin)

			if wasSpan {
				flushClose(h, m)
			} else {
				flushOpen(h, m)
			}

			bufHour = bufHour[:0]
			bufMin = bufMin[:0]
			wasSpan = false
			wasDump = true

			continue
		}

		if 'F' <= r && r <= 'W' || 'f' <= r && r <= 'w' {
			var (
				weekDay time.Weekday = -1
				next    rune
			)

			if len(layout) > i+1 {
				next = rune(layout[i+1])
			}

			switch r {
			case 'M', 'm':
				switch next {
				case 'o', 'O':
					weekDay = time.Monday
				}
			case 'T', 't':
				switch next {
				case 'u', 'U':
					weekDay = time.Tuesday
				case 'h', 'H':
					weekDay = time.Thursday
				}
			case 'W', 'w':
				switch next {
				case 'e', 'E':
					weekDay = time.Wednesday
				}
			case 'F', 'f':
				switch next {
				case 'r', 'R':
					weekDay = time.Friday
				}
			case 'S', 's':
				switch next {
				case 'a', 'A':
					weekDay = time.Saturday
				case 'u', 'U':
					weekDay = time.Sunday
				}
			}

			switch weekDay { //nolint:exhaustive
			case -1:
				continue
			case 0:
				weekDay = daysPerWeek // remap Sunday from Go's 0 to our 1-7 range
			}

			if wasDump {
				bufDay = bufDay[:0]
				wasDump = false
			}

			switch l, wd := len(bufDay), int(weekDay); {
			case wasSpan && l > 0 && bufDay[l-1] < wd:
				for d := bufDay[l-1] + 1; d <= wd; d++ {
					bufDay = append(bufDay, d)
				}

				wasSpan = false
			default:
				bufDay = append(bufDay, wd)
			}

			continue
		}

		if r == '-' {
			wasSpan = true
		}
	}

	if !wasDump && len(bufDay) > 0 {
		flushOpen(0, 0)
		flushClose(0, 0)
	}

	slices.SortFunc(ivs, func(a, b weekInterval) int {
		c := a.open - b.open
		if c != 0 {
			return c
		}

		return a.close - b.close
	})

	return ivs
}

func rtoi(r []rune) int {
	num := 0

	for _, v := range r {
		num = num*10 + int(v-'0')
	}

	return num
}

func matchWeek(ivs []weekInterval, tm int) bool {
	for _, iv := range ivs {
		if isOpenAt(iv, tm) {
			return true
		}
	}

	return false
}

// isOpenAt reports whether tm (minutes from Monday 00:00) falls within iv.
// The second branch handles Sunday→Monday overnight intervals where close > weekMins.
func isOpenAt(iv weekInterval, tm int) bool {
	if iv.open <= tm && tm < iv.close {
		return true
	}

	return iv.close > minsPerWeek && iv.open <= tm+minsPerWeek && tm+minsPerWeek < iv.close
}

// weekday converts Go's time.Weekday (Sunday=0) to the 1-7 range used
// internally (Monday=1 … Sunday=7).
func weekday(wd time.Weekday) int {
	if wd == time.Sunday {
		return daysPerWeek
	}

	return int(wd)
}

func weekMinutes(t time.Time) int {
	return (weekday(t.Weekday())-1)*minsPerDay + t.Hour()*minsPerHour + t.Minute()
}

func mondayOf(t time.Time) time.Time {
	wd := weekday(t.Weekday())

	return time.Date(t.Year(), t.Month(), t.Day()-(wd-1), 0, 0, 0, 0, t.Location())
}

func minsToTime(monday time.Time, mins int) time.Time {
	d := mins / minsPerDay
	rem := mins % minsPerDay

	return time.Date(monday.Year(), monday.Month(), monday.Day()+
		d, rem/minsPerHour, rem%minsPerHour, 0, 0, monday.Location())
}

// closeToTime converts a close interval value to time.Time.
// Close values stored as exact day boundaries (multiples of dayMins) represent
// "end of day" and are displayed as 23:59 of that day rather than 00:00 of the next.
// mins is always > 0 in practice; the guard is defensive.
func closeToTime(monday time.Time, mins int) time.Time {
	if mins > 0 && mins%minsPerDay == 0 {
		d := mins/minsPerDay - 1

		return time.Date(monday.Year(), monday.Month(), monday.Day()+d, 23, 59, 0, 0, monday.Location())
	}

	return minsToTime(monday, mins)
}
