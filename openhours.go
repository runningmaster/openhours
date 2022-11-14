package openhours

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Splitter contains auxiliary buffers and fields for parsing 'opening_hours'
// layout string.
type Splitter struct {
	input  bufio.Reader
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

	matchIndex *int
}

// NewSplitter creates a new Splitter.
func NewSplitter(t time.Time) *Splitter {
	return &Splitter{
		// input:    *bufio.NewReader(strings.NewReader("")),
		output:   make([]time.Time, 14),
		bufDay:   make([]int, 7),
		bufHour:  make([]rune, 2),
		bufMin:   make([]rune, 2),
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

func (s *Splitter) reset(r io.Reader) {
	s.input.Reset(r)
	s.output = s.output[:0]
	s.bufDay = s.bufDay[:0]
	s.bufHour = s.bufHour[:0]
	s.bufMin = s.bufMin[:0]
	s.matchIndex = nil
}

func (s *Splitter) parse(r io.Reader) error {
	s.reset(r)

	var (
		currRune, nextRune rune
		wasSpan, wasDump   bool
		err                error
	)

	for {
		currRune, _, err = s.input.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		if unicode.IsDigit(currRune) {
			switch len(s.bufHour) {
			case 0, 1:
				s.bufHour = append(s.bufHour, currRune)
				continue // =>
			}

			switch len(s.bufMin) {
			case 0, 1:
				s.bufMin = append(s.bufMin, currRune)
				if len(s.bufMin) == 1 {
					continue // =>
				}
			}

			h, _ := strconv.Atoi(string(s.bufHour))
			m, _ := strconv.Atoi(string(s.bufMin))

			for _, day := range s.bufDay {
				wd := int(s.tWeekDay)
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

				if wasSpan {
					switch {
					// fix -00:00
					case h == 0 && m == 0:
						h, m = 23, 59
					// fix -24:00
					case h == 24:
						h, m = 23, 59
					}
				}

				s.output = append(s.output, time.Date(s.tYear, s.tMonth,
					day, h, m, s.tSec, s.tNSec, s.tLoc),
				)
			}

			s.bufHour = s.bufHour[:0]
			s.bufMin = s.bufMin[:0]
			wasSpan = false
			wasDump = true

			continue // =>
		}

		if unicode.IsLetter(currRune) {
			nextRune, _, err = s.input.ReadRune()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}

			var weekDay time.Weekday = -1

			switch currRune {
			case 'M', 'm':
				switch nextRune {
				case 'o', 'O':
					weekDay = time.Monday
				}
			case 'T', 't':
				switch nextRune {
				case 'u', 'U':
					weekDay = time.Tuesday
				case 'h', 'H':
					weekDay = time.Thursday
				}
			case 'W', 'w':
				switch nextRune {
				case 'e', 'E':
					weekDay = time.Wednesday
				}
			case 'F', 'f':
				switch nextRune {
				case 'r', 'R':
					weekDay = time.Friday
				}
			case 'S', 's':
				switch nextRune {
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

		if currRune == '-' {
			wasSpan = true
		}
	}

	return nil
}

func (s *Splitter) setMatchIndex() {
	for i := 0; i < len(s.output); i++ {
		if s.output[i].After(s.t) {
			s.matchIndex = &i
			break
		}
	}
}

// Split partitions a layout string into a sorted slice of time.Time.
// Also it returns true in second param if initial time is in the open hours.
func (s *Splitter) Split(layout string) ([]time.Time, bool, error) {
	fix := func(s string) string {
		switch s {
		case "":
			return s
		case "24/7":
			return "Mo-Su 00:00-23:59"
		}

		if strings.Contains(s, ":") {
			return s
		}

		return s + " 00:00-23:59"
	}

	err := s.parse(strings.NewReader(fix(layout)))
	if err != nil {
		return nil, false, err
	}

	if len(s.output)%2 != 0 {
		return nil, false, fmt.Errorf("openhours: invalid input layout string %q", layout)
	}

	sort.Slice(s.output, func(i, j int) bool {
		return s.output[i].Before(s.output[j])
	})

	s.setMatchIndex()

	return s.output, s.matchIndex != nil && *s.matchIndex%2 == 1, nil
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
				if s.matchIndex != nil && *s.matchIndex == i {
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
