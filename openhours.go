package openhours

import (
	"errors"
	"sort"
	"strings"
	"time"
)

var ErrInvalidLayout = errors.New("openhours: invalid input layout string")

// Splitter contains auxiliary buffers and fields for parsing 'opening_hours'
// layout string.
type Splitter struct {
	output []time.Time

	bufDay  []int
	bufHour []rune
	bufMin  []rune

	t        time.Time
	tYear    int
	tMonth   time.Month
	tDay     int
	tWeekDay time.Weekday
	tHour    int
	tMin     int
	tSec     int
	tNSec    int
	tLoc     *time.Location
}

// NewSplitter creates a new Splitter.
func NewSplitter(t time.Time) *Splitter {
	const (
		len2  = 2
		len7  = 7
		len14 = 14
	)

	return &Splitter{
		output:   make([]time.Time, len14),
		bufDay:   make([]int, len7),
		bufHour:  make([]rune, len2),
		bufMin:   make([]rune, len2),
		t:        t,
		tYear:    t.Year(),
		tMonth:   t.Month(),
		tDay:     t.Day(),
		tWeekDay: t.Weekday(),
		tHour:    t.Hour(),
		tMin:     t.Minute(),
		tSec:     0,
		tNSec:    0,
		tLoc:     t.Location(),
	}
}

func (s *Splitter) reset() {
	s.output = s.output[:0]
	s.bufDay = s.bufDay[:0]
	s.bufHour = s.bufHour[:0]
	s.bufMin = s.bufMin[:0]
}

func (s *Splitter) parse(layout string) error {
	s.reset()

	if layout == "24/7" {
		layout = "Mo-Su"
	}

	dump := func(wd, day, h, m, ns int) {
		if wd == 0 {
			wd = 7
		}
		// shift month's day relative week's day
		switch {
		case day < wd:
			day = s.tDay - (wd - day)
		case day > wd:
			day = s.tDay + (day - wd)
		default:
			day = s.tDay
		}

		s.output = append(s.output, time.Date(s.tYear, s.tMonth,
			day, h, m, 0, ns, s.tLoc),
		)
	}

	var wasSpan, wasDump bool

	const (
		h23 = 23
		h24 = 24
		m59 = 59
		s1  = 1
	)

	for i, r := range layout {
		if '0' <= r && r <= '9' {
			switch len(s.bufHour) {
			case 0, 1:
				s.bufHour = append(s.bufHour, r)

				continue // =>
			}

			switch len(s.bufMin) {
			case 0, 1:
				s.bufMin = append(s.bufMin, r)
				if len(s.bufMin) == 1 {
					continue // =>
				}
			}

			h := rtoi(s.bufHour)
			m := rtoi(s.bufMin)

			ns := 0

			if wasSpan {
				switch {
				// fix -00:00
				case h == 0 && m == 0:
					h, m = h23, m59
				// fix -24:00
				case h == h24:
					h, m = h23, m59
				}

				ns = 1 // ns workaround for no need sort, see setMatchIndex
			}

			wd := int(s.tWeekDay)

			for _, day := range s.bufDay {
				dump(wd, day, h, m, ns)
			}

			s.bufHour = s.bufHour[:0]
			s.bufMin = s.bufMin[:0]
			wasSpan = false
			wasDump = true

			continue // =>
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

			switch weekDay {
			case -1:
				continue // =>
			case 0:
				weekDay = 7
			}

			if wasDump {
				s.bufDay = s.bufDay[:0]
				wasDump = false
			}

			switch l, wd := len(s.bufDay), int(weekDay); {
			case wasSpan && l > 0 && s.bufDay[l-1] < wd:
				// expand days in buffer to weekDay
				for i := s.bufDay[l-1] + 1; i <= wd; i++ {
					s.bufDay = append(s.bufDay, i)
				}

				wasSpan = false
			default:
				s.bufDay = append(s.bufDay, wd)
			}

			continue // =>
		}

		if r == '-' {
			wasSpan = true
		}
	}

	if len(s.output)%2 != 0 {
		return ErrInvalidLayout
	}

	if !wasDump && len(s.bufDay) > 0 {
		wd := int(s.tWeekDay)
		for _, day := range s.bufDay {
			dump(wd, day, 0, 0, 0)
			dump(wd, day, h23, m59, s1)
		}
	}

	return nil
}

func (s *Splitter) matchIndex() int {
	for i := 0; i < len(s.output); i++ {
		// ns workaround for no need sort
		if s.output[i].Weekday() != s.tWeekDay || s.output[i].Nanosecond() != 1 {
			continue
		}

		if s.output[i].After(s.t) {
			return i
		}
	}

	return -1
}

// Split partitions a layout string into a sorted slice of time.Time.
// Also it returns true in second param if initial time is in the open hours.
func (s *Splitter) Split(layout string) ([]time.Time, bool, error) {
	err := s.parse(layout)
	if err != nil {
		return nil, false, err
	}

	sort.Slice(s.output, func(i, j int) bool {
		return s.output[i].Before(s.output[j])
	})

	return s.output, s.matchIndex() > -1, nil
}

// Match returns true in second param if initial time is in the open hours.
func (s *Splitter) Match(layout string) (bool, error) {
	err := s.parse(layout)
	if err != nil {
		return false, err
	}

	return s.matchIndex() > -1, nil
}

// String implements fmt.Stringer to be printed for testing purposes.
// It invokes after Split.
func (s *Splitter) String() string {
	if len(s.output) == 0 {
		return ""
	}

	var (
		day int
		sb  strings.Builder
	)

	matchIndex := s.matchIndex()

	for i, v := range s.output {
		if day != v.Day() {
			if i != 0 {
				sb.WriteRune('\n')
			}

			sb.WriteString(v.Format("Mon, 02 Jan"))
			sb.WriteRune(' ')
			sb.WriteString(v.Format("15:04"))
		} else {
			switch i % 2 {
			case 0:
				sb.WriteRune(' ')
				sb.WriteString(v.Format("15:04"))
			case 1:
				if matchIndex == i {
					sb.WriteRune('*') // it is open
				} else {
					sb.WriteRune('-')
				}
				sb.WriteString(v.Format("15:04"))
			}
		}

		day = v.Day()
	}

	return sb.String()
}

func rtoi(r []rune) int {
	num := 0

	for i, r := range r {
		num += int(r - '0')
		if i == 0 {
			num *= 10
		}
	}

	return num
}
