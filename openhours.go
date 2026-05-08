package openhours

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	minsPerHour = 60
	minsPerDay  = 24 * minsPerHour // 1440
	minsPerWeek = 7 * minsPerDay   // 10080
	daysPerWeek = 7

	// DefaultCacheSize is the maximum number of distinct layout strings held
	// in the package-level parse cache before eviction via clock (second-chance).
	DefaultCacheSize = 4096
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
	value []weekInterval
	ref   atomic.Bool // referenced since last clock-hand pass
}

//nolint:gochecknoglobals
var (
	// CacheSize controls the maximum number of entries in the shared parse cache.
	// It must be set before the first call to NewSplitter.
	// The default is DefaultCacheSize (4096).
	CacheSize = DefaultCacheSize

	cacheMu  sync.RWMutex
	cache    map[string]int // layout → ring index
	ring     []clockEntry
	clockPos int

	initOnce sync.Once
)

// initCache pre-allocates the ring buffer on first use. It reads CacheSize
// at init time; later changes to CacheSize have no effect.
func initCache() {
	initOnce.Do(func() {
		n := CacheSize
		if n < 1 {
			n = DefaultCacheSize
		}

		cache = make(map[string]int, n)
		ring = make([]clockEntry, n)
	})
}

// Splitter parses and evaluates 'opening_hours' layout strings against a fixed
// reference time. Parsed results are memoized in a package-level cache shared
// across all Splitter instances. The cache is populated on first parse of each
// unique layout string and is never invalidated.
// A Splitter must not be used concurrently from multiple goroutines.
type Splitter struct {
	t      time.Time
	ivs    []weekInterval // last parsed result (read-only slice from global cache)
	output []time.Time    // pre-allocated buffer for Split return value
}

// NewSplitter returns a new Splitter with t as the reference time for open/closed evaluation.
func NewSplitter(t time.Time) *Splitter {
	initCache()

	return &Splitter{
		t:      t,
		output: make([]time.Time, 0, 14), //nolint:mnd
	}
}

// Reset updates the reference time, allowing Splitter reuse (e.g. via sync.Pool).
func (s *Splitter) Reset(t time.Time) {
	s.t = t
}

// Match reports whether the reference time falls within the open hours described by layout.
// An unparsable layout yields false — no error is returned.
func (s *Splitter) Match(layout string) bool {
	s.ivs = getOrParse(layout)

	return matchWeek(s.ivs, weekMinutes(s.t))
}

// Split parses layout and returns a sorted slice of open/close time boundaries
// anchored to the week containing the reference time.
// The second return value reports whether the reference time falls within open hours.
// An unparsable layout yields an empty slice and false — no error is returned.
// The returned slice is valid only until the next call to Split on the same Splitter;
// copy it if longer retention is needed.
func (s *Splitter) Split(layout string) ([]time.Time, bool) {
	s.ivs = getOrParse(layout)
	s.output = s.output[:0]

	monday := mondayOf(s.t)

	for _, iv := range s.ivs {
		s.output = append(s.output,
			minutesToTime(monday, iv.open),
			closeToTime(monday, iv.close),
		)
	}

	return s.output, matchWeek(s.ivs, weekMinutes(s.t))
}

// String implements fmt.Stringer. Must be called after Split or Match.
func (s *Splitter) String() string {
	if len(s.ivs) == 0 {
		return ""
	}

	monday := mondayOf(s.t)
	tm := weekMinutes(s.t)

	var (
		lastDay int
		sb      strings.Builder
	)

	for _, iv := range s.ivs {
		openT := minutesToTime(monday, iv.open)
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

// weekdayNum converts Go's time.Weekday (Sunday=0) to the 1-7 range used
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

func minutesToTime(monday time.Time, mins int) time.Time {
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

	return minutesToTime(monday, mins)
}

func getOrParse(layout string) []weekInterval {
	if layout == "24/7" {
		layout = "Mo-Su"
	}

	cacheMu.RLock()

	idx, ok := cache[layout]
	if ok {
		ring[idx].ref.Store(true)
		ivs := ring[idx].value

		cacheMu.RUnlock()

		return ivs
	}

	cacheMu.RUnlock()

	parsed := parse(layout)

	cacheMu.Lock()

	// Double-check: another goroutine may have raced us.
	idx, ok = cache[layout]
	if ok {
		ring[idx].ref.Store(true)
		ivs := ring[idx].value

		cacheMu.Unlock()

		return ivs
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

	ivs := parsed

	cacheMu.Unlock()

	return ivs
}

func parse(layout string) []weekInterval { //nolint:gocognit,gocyclo,cyclop
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
					closeBase += daysPerWeek
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
		if c := a.open - b.open; c != 0 {
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
