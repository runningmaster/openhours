package openhours_test

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
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
		{
			lstr: "Mo  -   foo    Su 11  :  bar    00  -    20:        00",
			want: true,
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

		// Empty layout: always closed, zero boundary.
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

// BenchmarkMatchMemo simulates filtering a large pharmacy list where many
// entries share the same layout string (realistic for chain pharmacies).
func BenchmarkMatchMemo(b *testing.B) {
	layouts := [...]string{
		"Mo-Fr 08:00-20:00; Sa-Su 09:00-18:00",
		"Mo-Su 08:00-22:00",
		"Mo-Fr 09:00-17:00",
		"24/7",
		"Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30",
	}

	now := time.Now()

	var ok bool

	for i := range b.N {
		ok = openhours.Match(layouts[i%len(layouts)], now)
		blackhole = ok
	}
}
