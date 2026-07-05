package openhours

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// Schedule holds a pre-parsed opening-hours schedule. The zero value is valid
// but will never match. Use Parse to obtain a Schedule from a string.
type Schedule struct{ ivs []weekInterval }

// ErrInvalidSchedule is returned by Parse when the spec is malformed or
// contains no recognisable schedule entries. Errors returned by Parse wrap it,
// adding the exact reason and byte offset; test with errors.Is.
var ErrInvalidSchedule = errors.New("openhours: invalid schedule")

// Parse compiles a spec into a Schedule for repeated evaluation.
// Unlike the package-level Match, Parse does not use the global cache;
// the caller owns the returned Schedule value.
//
// Parse is STRICT about time syntax and structure — a malformed schedule fails
// loudly instead of silently producing a plausible-but-wrong schedule (the
// worst failure mode for an "open now" filter). Times are H:MM or HH:MM with
// hour <= 24 and minute <= 59 (24:00 only as a close), every open time must be
// joined to its close with '-', and a time needs a preceding day group.
// Unknown WORDS, however, are ignored as noise ("PH off", localized labels),
// and the ',' ';' separators are transparent, so real-world
// OpenStreetMap-ish inputs still parse. A trailing day group without times
// means "open the whole day" (OSM semantics), and "24/7" (alone or with a
// trailing rule tail) is an alias for Mo-Su.
func Parse(spec string) (Schedule, error) {
	ivs, err := parse(spec)
	if err != nil {
		return Schedule{}, err
	}

	return Schedule{ivs: ivs}, nil
}

// Match reports whether t falls within the open hours described by l.
func (l Schedule) Match(t time.Time) bool {
	return matchWeek(l.ivs, weekMinutes(t))
}

// Split returns a sorted slice of open/close time boundaries anchored to the
// week containing t, together with a flag reporting whether t itself is open.
// Boundaries of overlapping intervals are interleaved in time order.
func (l Schedule) Split(t time.Time) ([]time.Time, bool) {
	monday := mondayOf(t)
	out := make([]time.Time, 0, len(l.ivs)*2)

	for _, iv := range l.ivs {
		out = append(out, minsToTime(monday, iv.open), closeToTime(monday, iv.close))
	}

	// Intervals are sorted by open, but a close may outlast the next open
	// ("Mo 08:00-20:00 10:00-12:00"), so the interleaved slice needs a sort.
	slices.SortFunc(out, time.Time.Compare)

	return out, matchWeek(l.ivs, weekMinutes(t))
}

// Format returns a human-readable summary of the schedule for the week
// containing t, marking the currently-open interval with '*'.
func (l Schedule) Format(t time.Time) string {
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
// If t falls within an open interval, open is true and boundary is when the
// UNION of overlapping/adjacent intervals containing t closes — a schedule like
// "Mo 08:00-12:00 12:00-20:00" reports 20:00 at 09:00, not the 12:00 seam. The
// union also chains across the Su→Mo week boundary, so "Fr-Mo" at Sunday
// evening reports Tuesday 00:00 rather than the Monday-midnight seam.
// If t falls in a gap, open is false and boundary is when it next opens.
// boundary is the zero Time if the schedule has no intervals, or if the union
// covers the whole week (a 24/7 schedule): the schedule never closes.
// Note: boundary is the exact transition instant — a midnight close is 00:00, not 23:59.
func (l Schedule) Until(t time.Time) (open bool, boundary time.Time) {
	if len(l.ivs) == 0 {
		return false, time.Time{}
	}

	tm := weekMinutes(t)
	monday := mondayOf(t)

	if end, ok := unionEnd(l.ivs, tm); ok {
		if end-tm >= minsPerWeek { // union wrapped a full week: always open
			return true, time.Time{}
		}

		return true, minsToTime(monday, end)
	}

	// Overnight Sunday→Monday: close exceeds minsPerWeek; fold back to this week's Monday.
	if end, ok := unionEnd(l.ivs, tm+minsPerWeek); ok {
		return true, minsToTime(monday, end-minsPerWeek)
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

// unionEnd returns the close of the union of intervals covering tm: starting
// from the widest interval containing tm, it keeps extending the boundary while
// another interval overlaps or touches it. Each interval is also considered
// shifted one week forward, so the union chains across the Su→Mo week boundary.
// Reports false when tm is not open. A result with end-tm >= minsPerWeek means
// the union wrapped a full week — the schedule is open at every minute.
func unionEnd(ivs []weekInterval, tm int) (int, bool) {
	end := -1

	for _, iv := range ivs {
		if iv.open <= tm && tm < iv.close && iv.close > end {
			end = iv.close
		}
	}

	if end < 0 {
		return 0, false
	}

	for extended := true; extended && end-tm < minsPerWeek; {
		extended = false

		for _, iv := range ivs {
			for _, shift := range [...]int{0, minsPerWeek} {
				if o, c := iv.open+shift, iv.close+shift; o <= end && c > end {
					end = c
					extended = true
				}
			}
		}
	}

	return end, true
}

// Match reports whether t falls within the open hours described by spec.
// An unparsable spec yields false — no error is returned.
// Results are cached in a package-level cache; see SetCacheSize.
func Match(spec string, t time.Time) bool {
	initCache()

	return getOrParse(spec).Match(t)
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
	value Schedule
	ref   atomic.Bool // referenced since last clock-hand pass
}

//nolint:gochecknoglobals
var (
	cacheSize atomic.Int32

	cacheMu  sync.RWMutex
	cache    map[string]int // spec → ring index
	ring     []clockEntry
	clockPos int

	initOnce sync.Once
)

func init() { //nolint: gochecknoinits
	cacheSize.Store(defaultCacheSize)
}

// SetCacheSize sets the maximum number of distinct spec strings held in the
// package-level parse cache. It must be called before the first call to Match;
// later calls have no effect once the cache is initialised.
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

func getOrParse(spec string) Schedule {
	cacheMu.RLock()

	idx, ok := cache[spec]
	if ok {
		ring[idx].ref.Store(true)
		l := ring[idx].value

		cacheMu.RUnlock()

		return l
	}

	cacheMu.RUnlock()

	// A malformed spec caches as the zero Schedule, which never matches —
	// the package-level Match stays error-free by contract.
	ivs, _ := parse(spec)
	parsed := Schedule{ivs: ivs}

	cacheMu.Lock()

	// Double-check: another goroutine may have raced us.
	idx, ok = cache[spec]
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

	ring[clockPos].key = spec
	ring[clockPos].value = parsed
	ring[clockPos].ref.Store(true)
	cache[spec] = clockPos

	clockPos = (clockPos + 1) % len(ring)

	cacheMu.Unlock()

	return parsed
}

// alias247 rewrites a leading "24/7" token (alone, or followed by a rule tail
// like "; PH off") to its "Mo-Su" full-week equivalent. Any other placement is
// left to the parser, which rejects it as a malformed time.
func alias247(spec string) string {
	s := strings.TrimSpace(spec)

	rest, ok := strings.CutPrefix(s, "24/7")
	if !ok {
		return spec
	}

	if rest != "" {
		r, _ := utf8.DecodeRuneInString(rest)
		if r != ' ' && r != '\t' && r != ';' && r != ',' {
			return spec
		}
	}

	return "Mo-Su" + rest
}

// parse compiles a spec into sorted week intervals. Strictness
// contract: time syntax and open-close structure are validated (see Parse);
// unknown words and the ',' ';' separators are transparent noise. A group is a
// day set plus the intervals that follow it; a day token after intervals starts
// the next group; a trailing group without intervals emits full days.
func parse(spec string) ([]weekInterval, error) { //nolint:gocognit,gocyclo,cyclop,funlen
	s := alias247(spec)

	var (
		days      []int // current group's day set (1=Mo … 7=Su)
		ivs       []weekInterval
		openMin   = -1 // pending open time; -1 = none
		wantClose bool // '-' consumed after the open time, close expected
		dayRange  bool // '-' consumed after a day token, range end expected
		lastDay   bool // previous meaningful token was a day (for '-' meaning)
		haveTimes bool // current group already emitted intervals
	)

	// emit appends one interval per day of the current group, handling the
	// 00:00/24:00 end-of-day sentinels and overnight (close <= open) wraps.
	// Equal open and close span a full 24 hours (OSM semantics: "08:00-08:00"
	// closes at 08:00 the next day); the sentinels never collide with this,
	// since an open time of 24:00 is rejected earlier.
	emit := func(closeH, closeM int) {
		const h24 = 24

		var closeMinOfDay int

		if (closeH == 0 && closeM == 0) || closeH == h24 {
			closeMinOfDay = minsPerDay
		} else {
			closeMinOfDay = closeH*minsPerHour + closeM
		}

		isOvernight := closeMinOfDay <= openMin

		for _, day := range days {
			base := (day - 1) * minsPerDay

			closeBase := base

			if isOvernight {
				closeBase = (day % daysPerWeek) * minsPerDay
				if closeBase < base {
					closeBase += minsPerWeek
				}
			}

			ivs = append(ivs, weekInterval{open: base + openMin, close: closeBase + closeMinOfDay})
		}
	}

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])

		switch {
		case unicode.IsLetter(r):
			j := i

			for j < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[j:])
				if !unicode.IsLetter(r2) {
					break
				}

				j += sz
			}

			word := s[i:j]
			i = j

			wd := dayOf(word)
			if wd == 0 {
				continue // unknown word: transparent noise ("PH", "off", localized labels)
			}

			if openMin >= 0 || wantClose {
				return nil, errAt(i, "dangling open time before day %q", word)
			}

			// A day token after a timed group starts the next group.
			if haveTimes && !dayRange {
				days = days[:0]
				haveTimes = false
			}

			if dayRange {
				if len(days) == 0 {
					return nil, errAt(i, "day range with no start day")
				}

				days = expandRange(days, wd)
				dayRange = false
			} else {
				days = append(days, wd)
			}

			lastDay = true

		case '0' <= r && r <= '9':
			h, m, j, err := readTime(s, i)
			if err != nil {
				return nil, err
			}

			i = j
			lastDay = false

			switch {
			case wantClose:
				emit(h, m)

				openMin = -1
				wantClose = false
				haveTimes = true
			case openMin >= 0:
				return nil, errAt(i, "expected '-' between open and close times")
			default:
				if len(days) == 0 {
					return nil, errAt(i, "time without a preceding day group")
				}

				if dayRange {
					return nil, errAt(i, "unfinished day range before time")
				}

				if h == maxHour {
					return nil, errAt(i, "24:00 is valid only as a close time")
				}

				openMin = h*minsPerHour + m
			}

		case r == '-':
			switch {
			case openMin >= 0 && !wantClose:
				wantClose = true
			case lastDay:
				dayRange = true
			default:
				return nil, errAt(i, "unexpected '-'")
			}

			i += size

		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == ';':
			i += size

		default:
			return nil, errAt(i, "unexpected character %q", r)
		}
	}

	if openMin >= 0 || wantClose {
		return nil, errAt(len(s), "dangling open time at end of spec")
	}

	if dayRange {
		return nil, errAt(len(s), "unfinished day range at end of spec")
	}

	// Trailing day group without times: open the whole day (OSM semantics;
	// this is also what the "24/7" → "Mo-Su" alias expands to).
	if len(days) > 0 && !haveTimes {
		openMin = 0
		emit(0, 0)
	}

	if len(ivs) == 0 {
		return nil, fmt.Errorf("%w: no schedule entries", ErrInvalidSchedule)
	}

	slices.SortFunc(ivs, func(a, b weekInterval) int {
		c := a.open - b.open
		if c != 0 {
			return c
		}

		return a.close - b.close
	})

	return ivs, nil
}

const (
	maxHour = 24
	maxMin  = 59
)

// readTime reads a strict time token at s[i:]: one or two hour digits, ':',
// exactly two minute digits, all contiguous. Validates hour <= 24 (24 only as
// 24:00) and minute <= 59. Returns the position just past the token.
func readTime(s string, i int) (h, m, next int, err error) {
	j := i
	for j < len(s) && j-i < 2 && '0' <= s[j] && s[j] <= '9' {
		h = h*10 + int(s[j]-'0')
		j++
	}

	if j < len(s) && '0' <= s[j] && s[j] <= '9' {
		return 0, 0, 0, errAt(i, "hour has more than two digits")
	}

	if j >= len(s) || s[j] != ':' {
		return 0, 0, 0, errAt(i, "time %q: expected ':' after hour", s[i:j])
	}

	j++

	k := j
	for k < len(s) && k-j < 2 && '0' <= s[k] && s[k] <= '9' {
		m = m*10 + int(s[k]-'0')
		k++
	}

	if k-j != 2 { //nolint:mnd
		return 0, 0, 0, errAt(i, "minutes must be exactly two digits")
	}

	if h > maxHour || m > maxMin || (h == maxHour && m != 0) {
		return 0, 0, 0, errAt(i, "clock value %02d:%02d out of range", h, m)
	}

	return h, m, k, nil
}

// expandRange appends the days between the current last day and wd inclusive,
// wrapping across the week end: "Fr-Mo" expands to Fr, Sa, Su, Mo.
func expandRange(days []int, wd int) []int {
	prev := days[len(days)-1]

	switch {
	case wd > prev:
		for d := prev + 1; d <= wd; d++ {
			days = append(days, d)
		}
	case wd < prev:
		for d := prev + 1; d <= daysPerWeek; d++ {
			days = append(days, d)
		}

		for d := 1; d <= wd; d++ {
			days = append(days, d)
		}
	default: // "Mo-Mo": the single day is already in the set
	}

	return days
}

// errAt builds an ErrInvalidSchedule-wrapped error carrying the byte offset of
// the offending token, so ingest pipelines can report exactly what is wrong.
func errAt(off int, format string, args ...any) error {
	return fmt.Errorf("%w: %s (offset %d)", ErrInvalidSchedule, fmt.Sprintf(format, args...), off)
}

// dayOf maps a two-letter day abbreviation (case-insensitive) to 1=Mo … 7=Su,
// or 0 when word is not a day token.
func dayOf(word string) int {
	if len(word) != 2 { //nolint:mnd
		return 0
	}

	b0, b1 := word[0]|0x20, word[1]|0x20

	switch {
	case b0 == 'm' && b1 == 'o':
		return 1
	case b0 == 't' && b1 == 'u':
		return 2
	case b0 == 'w' && b1 == 'e':
		return 3
	case b0 == 't' && b1 == 'h':
		return 4
	case b0 == 'f' && b1 == 'r':
		return 5
	case b0 == 's' && b1 == 'a':
		return 6
	case b0 == 's' && b1 == 'u':
		return 7
	default:
		return 0
	}
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
