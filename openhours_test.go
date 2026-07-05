package openhours_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runningmaster/openhours"
)

func TestSplitMatch(t *testing.T) {
	tests := [...]struct {
		lstr string
		want bool
	}{
		{
			lstr: "24/7",
			want: true,
		},
		{
			lstr: "Mo 09:00-14:00 Tu-Fr 00:00-00:00 ",
			want: true,
		},

		{
			lstr: "Su-mo",
			want: false,
		},
		{
			lstr: "Mo-Fr 08:00-21:00; Sa 08:30-20:00; Su 09:00-19:00",
			want: true,
		},
		{
			lstr: "Mo-Tu, Fr 14:00-17:00 08:00-12:00 We 08:00-13:00 14:00-18:00 Th, Sa-Su 00:00-00:00",
			want: true,
		},

		{
			lstr: "Mo, Su 07:30-20:00; Tu-Sa 07:30-20:30",
			want: true,
		},

		{
			lstr: "Su 06:00-07:00 07:30-21:00 22:00-23:00",
			want: false,
		},
		{
			lstr: "foobar",
			want: false,
		},
		{
			lstr: "foo 06:00-07:00 bar 22:00-23:00",
			want: false,
		},
		{
			lstr: "",
			want: false,
		},
		{
			lstr: "Mo 09:00*14:00",
			want: false,
		},
		{
			lstr: "Mo 09:00 14:00",
			want: false,
		},
		{
			lstr: "Mo-Fr09:00-17:31;Sa09:00-00:00;Su00:00-00:00",
			want: true,
		},
		{
			lstr: "Mo-Sa 08:00-22:00; Su 08:00-21:00",
			want: true,
		},
		{
			lstr: "Mo-Su 11:00-17:00",
			want: false,
		},
		// Strict times: junk inside a time token is a parse error now, not a
		// best-effort guess (this string used to lenient-parse as Mo-Su 11:00-20:00).
		{
			lstr: "Mo  -   foo    Su 11  :  bar    00  -    20:        00",
			want: false,
		},
		// Single-digit hour is valid strict syntax (H:MM).
		{
			lstr: "Mo-Fr 9:00-18:00",
			want: true, // Wednesday 17:30
		},
		// Out-of-range clock values are rejected (used to lenient-parse into garbage).
		{
			lstr: "Mo-Fr 08:00-25:70",
			want: false,
		},
		// Wrapping day range: We-Mo covers We,Th,Fr,Sa,Su,Mo.
		{
			lstr: "We-Mo 08:00-20:00",
			want: true, // Wednesday 17:30
		},
		// "24/7" with a rule tail; the tail words are transparent noise.
		{
			lstr: "24/7; PH off",
			want: true,
		},
		// Unknown words are noise even between day groups and times.
		{
			lstr: "Mo-Fr 08:00-20:00 PH off",
			want: true, // Wednesday 17:30
		},
		// Trailing day group without times = open the whole day.
		{
			lstr: "Mo-Tu 08:00-12:00 We",
			want: true, // Wednesday 17:30
		},
		{
			lstr: "Mo-Th 08:00-17:00; Fr 08:00-18:00; Sa 08:00-13:00",
			want: false,
		},
		{
			lstr: "Sa-Su 00:00-24:00",
			want: false,
		},
		{
			lstr: "Mo-Tu 08:00-17:00; We-Th, Fr, Sa-Su",
			want: true,
		},
		{
			lstr: "Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30",
			want: true,
		},
		{
			lstr: "We 18:00-21:00",
			want: false,
		},
		// overnight: close time on next calendar day
		{
			lstr: "We 08:00-01:00",
			want: true, // 17:30 is between Wed 08:00 and Thu 01:00
		},
		{
			lstr: "We 20:00-01:00",
			want: false, // 17:30 is before Wed 20:00
		},
		{
			lstr: "Mo-Su 08:00-02:00",
			want: true, // every day open until 02:00 next day
		},
		// close == open spans a full 24 hours (OSM semantics)
		{
			lstr: "We 08:00-08:00",
			want: true, // Wed 08:00 through Thu 08:00 includes Wed 17:30
		},
		{
			lstr: "Tu 18:00-18:00",
			want: true, // Tue 18:00 through Wed 18:00 includes Wed 17:30
		},
		{
			lstr: "Th 08:00-08:00",
			want: false, // opens Thu 08:00, after Wed 17:30
		},
	}

	// Separate fixed-time tests: Wednesday 23:59 — last minute of day must be "open".
	eodTests := [...]struct {
		lstr string
		want bool
	}{
		{lstr: "We 00:00-00:00", want: true},    // full day via 00:00 sentinel
		{lstr: "We 00:00-24:00", want: true},    // full day via 24:00 sentinel
		{lstr: "Mo-Su 08:00-00:00", want: true}, // open until midnight, last minute included
	}

	now := time.Now()
	day := now.Day()

	switch now.Weekday() {
	case time.Sunday:
		day -= 4
	case time.Monday:
		day += 2
	case time.Tuesday:
		day++
	case time.Wednesday:
		// noop.
	case time.Thursday:
		day--
	case time.Friday:
		day -= 2
	case time.Saturday:
		day -= 3
	}

	// Wednesday 17:30
	now = time.Date(now.Year(), now.Month(), day, 17, 30, 0, 0, now.Location())

	for _, test := range tests {
		t.Run(test.lstr, func(t *testing.T) {
			l, _ := openhours.Parse(test.lstr)

			_, ok := l.Split(now)
			if ok != test.want {
				t.Errorf("split: case %q: got %v, want %v", test.lstr, ok, test.want)
			}

			ok = l.Match(now)
			if ok != test.want {
				t.Errorf("match: case %q: got %v, want %v", test.lstr, ok, test.want)
			}
		})
	}

	// Wednesday 23:59 — the last minute of day must be included in open window.
	eodNow := time.Date(now.Year(), now.Month(), day, 23, 59, 0, 0, now.Location())

	for _, test := range eodTests {
		t.Run("eod/"+test.lstr, func(t *testing.T) {
			l, _ := openhours.Parse(test.lstr)

			_, ok := l.Split(eodNow)
			if ok != test.want {
				t.Errorf("split eod: case %q: got %v, want %v", test.lstr, ok, test.want)
			}

			ok = l.Match(eodNow)
			if ok != test.want {
				t.Errorf("match eod: case %q: got %v, want %v", test.lstr, ok, test.want)
			}
		})
	}
}

// TestMatchAt pins that MatchAt(TimeOf(t)) is exactly equivalent to Match(t)
// across schedules and moments, including overnight and week-wrap intervals.
func TestMatchAt(t *testing.T) {
	specs := []string{
		"24/7",
		"Mo-Fr 09:00-20:00",
		"Su 22:00-02:00",    // overnight Su→Mo
		"Mo-Su 08:00-02:00", // overnight every day
		"We 08:00-08:00",    // 24h wrap
		"Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30",
		"Mo-Tu 08:00-12:00 We", // trailing full day
	}

	// Every 17 minutes across a full week (Mon 7 Nov 2022 …).
	start := time.Date(2022, time.November, 7, 0, 0, 0, 0, time.Local)

	for _, spec := range specs {
		l, err := openhours.Parse(spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", spec, err)
		}

		for m := 0; m < 7*24*60; m += 17 {
			at := start.Add(time.Duration(m) * time.Minute)

			if got, want := l.MatchAt(openhours.TimeOf(at)), l.Match(at); got != want {
				t.Errorf("%q at %s: MatchAt = %v, Match = %v",
					spec, at.Format("Mon 15:04"), got, want)
			}
		}
	}
}

// TestMatchConcurrent hammers the package-level cache from many goroutines
// with more unique specs than the default cache size, forcing constant clock
// eviction, and checks every answer against an uncached Parse+Match. Run with
// -race this also proves the lock-free hit path publishes entries safely.
func TestMatchConcurrent(t *testing.T) {
	now := time.Now()
	days := [...]string{"Mo", "Tu", "We", "Th", "Fr"}

	// ~7200 unique valid specs (> default cache size 4096) plus some garbage.
	specs := make([]string, 0, 7500)
	for i := range 7200 {
		specs = append(specs, fmt.Sprintf("%s %d:%02d-23:00", days[i/1440], i%1440/60, i%60))
	}

	for i := range 300 {
		specs = append(specs, fmt.Sprintf("bogus%d", i))
	}

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range 2000 {
				spec := specs[(i*13+w*991)%len(specs)]

				want := false
				if l, err := openhours.Parse(spec); err == nil {
					want = l.Match(now)
				}

				if got := openhours.Match(spec, now); got != want {
					t.Errorf("Match(%q) = %v, want %v", spec, got, want)
				}
			}
		}()
	}

	wg.Wait()
}

// TestSplitSorted pins the doc contract: Split returns boundaries in time
// order even when intervals overlap or nest.
func TestSplitSorted(t *testing.T) {
	specs := []string{
		"Mo 08:00-20:00 10:00-12:00",               // nested
		"Mo 08:00-12:00 10:00-20:00",               // overlapping
		"Su 22:00-02:00 Mo 01:00-03:00",            // overnight wrap overlap
		"Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00", // disjoint
		"Mo-Tu 08:00-12:00 We",                     // full-day close (23:59)
	}

	now := time.Date(2022, time.November, 9, 17, 30, 0, 0, time.Local)

	for _, spec := range specs {
		l, err := openhours.Parse(spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", spec, err)
		}

		ts, _ := l.Split(now)
		if !slices.IsSortedFunc(ts, time.Time.Compare) {
			t.Errorf("Split(%q): boundaries not sorted: %v", spec, ts)
		}
	}
}

// TestErrOffset pins that parse errors report the byte offset of the
// offending token itself, not the position just past it.
func TestErrOffset(t *testing.T) {
	tests := []struct {
		lstr string
		want string
	}{
		// Second time token "14:00" starts at byte 9.
		{"Mo 09:00 14:00", "(offset 9)"},
		// The time itself is the offending token, at byte 0.
		{"09:00-18:00", "(offset 0)"},
		// Day token "Sa" interrupting a dangling open time, at byte 12.
		{"Mo-Fr 08:00 Sa 10:00-12:00", "(offset 12)"},
		// "24:00" as an open time, at byte 3.
		{"Mo 24:00-06:00", "(offset 3)"},
		// readTime errors already pointed at the token start; keep it that way.
		{"Mo 8:0-20:00", "(offset 3)"},
	}

	for _, test := range tests {
		_, err := openhours.Parse(test.lstr)
		if err == nil {
			t.Errorf("Parse(%q): expected error", test.lstr)
			continue
		}

		if !strings.Contains(err.Error(), test.want) {
			t.Errorf("Parse(%q): err = %v, want offset marker %q", test.lstr, err, test.want)
		}
	}
}

// TestDuplicateDays pins that duplicate day tokens and repeated groups do not
// emit duplicate intervals: "Mo,Mo-Tu ...; Mo ..." is the same schedule as
// "Mo-Tu ..." for Format, Split and Match alike.
func TestDuplicateDays(t *testing.T) {
	now := time.Date(2022, time.November, 9, 17, 30, 0, 0, time.Local)

	dup, err := openhours.Parse("Mo,Mo-Tu 08:00-12:00; Mo 08:00-12:00")
	if err != nil {
		t.Fatal(err)
	}

	plain, err := openhours.Parse("Mo-Tu 08:00-12:00")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := dup.Format(now), plain.Format(now); got != want {
		t.Errorf("Format:\ngot  %q\nwant %q", got, want)
	}

	ts, _ := dup.Split(now)
	if len(ts) != 4 {
		t.Errorf("Split: got %d boundaries, want 4: %v", len(ts), ts)
	}
}

// TestParseStrict pins the strictness contract: malformed time syntax and
// structure fail with ErrInvalidSchedule instead of silently producing a
// plausible-but-wrong schedule, while noise words and separators stay legal.
func TestParseStrict(t *testing.T) {
	invalid := []string{
		"Mo-Fr 08:00-25:70",  // hour and minutes out of range
		"Mo-Fr 08:00-08:60",  // minutes out of range
		"Mo-Fr 24:30-08:00",  // 24:xx only as 24:00
		"Mo 24:00-06:00",     // 24:00 only as a close time
		"Mo-Fr 08:00",        // dangling open time
		"Mo-Fr 08:00-",       // dangling open time (dash, no close)
		"Mo 09:00 14:00",     // two times without '-'
		"Mo 09:00*14:00",     // unknown punctuation
		"Mo 8:0-20:00",       // minutes must be two digits
		"Mo 0800-2000",       // colon is required
		"Mo 123:00-20:00",    // hour has more than two digits
		"09:00-18:00",        // time without a preceding day group
		"Mo 08:00-12:00 Fr-", // unfinished day range at end
		"foobar",             // no schedule entries
		"",                   // no schedule entries
	}

	for _, lstr := range invalid {
		if _, err := openhours.Parse(lstr); !errors.Is(err, openhours.ErrInvalidSchedule) {
			t.Errorf("Parse(%q): err = %v, want ErrInvalidSchedule", lstr, err)
		}
	}

	valid := []string{
		"Mo-Fr 9:00-18:00",                  // single-digit hour
		"24/7",                              // alias
		"24/7; PH off",                      // alias with rule tail
		"Mo-Fr 08:00-20:00; PH off",         // noise words
		"We-Mo 08:00-20:00",                 // wrapping day range
		"Mo-Sa 08:00-21:00 Su",              // trailing full-day group
		"Mo-Fr08:00-13:00,14:00-18:00",      // no spaces, comma separator
		"Mo-Su 08:00-02:00",                 // overnight
		"Mo 09:00-14:00 Tu-Fr 00:00-00:00 ", // trailing space
	}

	for _, lstr := range valid {
		if _, err := openhours.Parse(lstr); err != nil {
			t.Errorf("Parse(%q): unexpected err = %v", lstr, err)
		}
	}
}

func TestString(t *testing.T) {
	tests := [...]struct {
		lstr string
		want string
	}{
		{
			lstr: "24/7",
			want: `Mon, 07 Nov 00:00-23:59
Tue, 08 Nov 00:00-23:59
Wed, 09 Nov 00:00-23:59
Thu, 10 Nov 00:00-23:59
Fri, 11 Nov 00:00-23:59
Sat, 12 Nov 00:00-23:59
Sun, 13 Nov 00:00*23:59`,
		},
		{
			lstr: "Mo 09:00-14:00 Tu-Fr 00:00-24:00",
			want: `Mon, 07 Nov 09:00-14:00
Tue, 08 Nov 00:00-23:59
Wed, 09 Nov 00:00-23:59
Thu, 10 Nov 00:00-23:59
Fri, 11 Nov 00:00-23:59`,
		},
	}

	// November 13 17:30
	now := time.Now()
	now = time.Date(2022, time.November, 13, 17, 30, 0, 0, now.Location())

	for _, test := range tests {
		t.Run(test.lstr, func(t *testing.T) {
			l, err := openhours.Parse(test.lstr)
			if err != nil {
				t.Fatalf("Parse(%q): %v", test.lstr, err)
			}

			if got := l.Format(now); got != test.want {
				t.Errorf("case %q:\ngot  %v\nwant %v", test.lstr, got, test.want)
			}
		})
	}
}

func TestTestdata(t *testing.T) {
	const testFile = "./testdata/openhours"

	b, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	scanner := bufio.NewScanner(bytes.NewReader(b))

	for scanner.Scan() {
		line := scanner.Text()
		l, _ := openhours.Parse(line)

		ok1 := l.Match(now)
		_, ok2 := l.Split(now)

		if ok1 != ok2 {
			t.Fatal("testdata: split.ok != match.ok")
		}

		t.Log(line + "\n=\n" + l.Format(now))
	}

	err = scanner.Err()
	if err != nil {
		t.Fatal(err)
	}
}

func Example() { //nolint:testableexamples
	now := time.Now()

	fmt.Printf("%s\n\n", now.Format("Mon, 02 Jan 15:04"))

	for _, v := range []string{
		"Mo-Tu, Fr 08:00-12:00 14:00-17:00 We 08:00-08:00 Th, Sa-Su 00:00-00:00",
		"Mo-Th 08:00-17:00; Fr 08:00-18:00; Sa 08:00-13:00",
	} {
		l, _ := openhours.Parse(v)

		fmt.Printf("%s\n%s %v\n\n", v, l.Format(now), l.Match(now))
	}
}

func TestUntil(t *testing.T) {
	loc := time.Now().Location()
	d := func(day, hour, min int) time.Time {
		return time.Date(2022, time.November, day, hour, min, 0, 0, loc)
	}

	// November 2022: Mon 7 … Sun 13.
	tests := []struct {
		lstr      string
		at        time.Time
		wantOpen  bool
		wantBound time.Time
	}{
		// Open: returns close time of current interval.
		{"Mo-Fr 09:00-20:00", d(9, 17, 30), true, d(9, 20, 0)},

		// Open: midnight close returned as 00:00 next day (not 23:59).
		{"Mo 09:00-00:00", d(7, 17, 30), true, d(8, 0, 0)},

		// Closed: next opening later this week.
		{"Mo-Fr 09:00-17:00", d(9, 17, 30), false, d(10, 9, 0)},

		// Closed: no more openings this week, wraps to next Monday.
		{"Mo-Fr 09:00-17:00", d(12, 20, 0), false, d(14, 9, 0)},

		// Overnight Sunday→Monday: open via overflow branch.
		{"Su 22:00-02:00", d(7, 1, 0), true, d(7, 2, 0)},

		// Overnight Sunday→Monday: closed after the interval, next is this Sunday.
		{"Su 22:00-02:00", d(7, 3, 0), false, d(13, 22, 0)},

		// Overlapping intervals: boundary is the union close, not the first seam.
		{"Mo 08:00-12:00 10:00-20:00", d(7, 9, 0), true, d(7, 20, 0)},

		// Adjacent intervals chain: reopening at the same minute is no seam.
		{"Mo 08:00-12:00 12:00-20:00", d(7, 9, 0), true, d(7, 20, 0)},

		// Union chains across the Su→Mo week boundary: separately-encoded
		// Sunday close and Monday open are no seam.
		{"Su 22:00-24:00 Mo 00:00-02:00", d(13, 23, 0), true, d(14, 2, 0)},

		// Full-day wrap: Fr-Mo at Sunday evening stays open through Monday.
		{"Fr-Mo", d(13, 23, 0), true, d(15, 0, 0)},

		// Overnight interval chains into the next Monday interval.
		{"Su 22:00-02:00 Mo 02:00-04:00", d(7, 1, 0), true, d(7, 4, 0)},

		// close == open is a full 24 hours: closes same time next day.
		{"Mo 08:00-08:00", d(7, 9, 0), true, d(8, 8, 0)},

		// Always open: never closes, zero boundary.
		{"24/7", d(13, 23, 0), true, time.Time{}},

		// Empty spec: always closed, zero boundary.
		{"", d(9, 17, 30), false, time.Time{}},
	}

	for _, test := range tests {
		t.Run(test.lstr+"@"+test.at.Format("Mon 15:04"), func(t *testing.T) {
			l, _ := openhours.Parse(test.lstr)
			open, bound := l.Until(test.at)

			if open != test.wantOpen {
				t.Errorf("open: got %v, want %v", open, test.wantOpen)
			}

			if !bound.Equal(test.wantBound) {
				t.Errorf("boundary: got %v, want %v",
					bound.Format("Mon 02 Jan 15:04"),
					test.wantBound.Format("Mon 02 Jan 15:04"))
			}
		})
	}
}

var blackhole bool //nolint:gochecknoglobals

func BenchmarkSplit(b *testing.B) {
	now := time.Now()
	l, _ := openhours.Parse("Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30")

	var ok bool

	for range b.N {
		_, ok = l.Split(now)
		blackhole = ok
	}
}

func BenchmarkMatch(b *testing.B) {
	now := time.Now()

	var ok bool

	for range b.N {
		ok = openhours.Match("Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30", now)
		blackhole = ok
	}
}

// BenchmarkMatchAt is the batch hot path: the instant is decomposed once per
// request with TimeOf, each point costs only the interval scan.
func BenchmarkMatchAt(b *testing.B) {
	l, _ := openhours.Parse("Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30")
	wt := openhours.TimeOf(time.Now())

	var ok bool

	for range b.N {
		ok = l.MatchAt(wt)
		blackhole = ok
	}
}

// BenchmarkMatchParallel pins the scaling of the cache hit path: hits are
// lock-free, so throughput must grow with cores, not collapse on a mutex.
func BenchmarkMatchParallel(b *testing.B) {
	now := time.Now()

	var sink atomic.Bool

	b.RunParallel(func(pb *testing.PB) {
		var ok bool
		for pb.Next() {
			ok = openhours.Match("Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30", now)
		}

		sink.Store(ok)
	})
}

// BenchmarkMatchMemo simulates filtering a large pharmacy list where many
// entries share the same spec string (realistic for chain pharmacies).
func BenchmarkMatchMemo(b *testing.B) {
	specs := [...]string{
		"Mo-Fr 08:00-20:00; Sa-Su 09:00-18:00",
		"Mo-Su 08:00-22:00",
		"Mo-Fr 09:00-17:00",
		"24/7",
		"Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30",
	}

	now := time.Now()

	var ok bool

	for i := range b.N {
		ok = openhours.Match(specs[i%len(specs)], now)
		blackhole = ok
	}
}
