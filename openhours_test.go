package openhours_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
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
			lstr: "Mo 09:00-14:00 Tu-Fr 00:00-00:00",
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
	}

	now := time.Now()
	day := now.Day()

	switch now.Weekday() {
	case time.Sunday:
		day = day - 4
	case time.Monday:
		day = day + 2
	case time.Tuesday:
		day = day + 1
		//	case time.Wednesday:
	case time.Thursday:
		day = day - 1
	case time.Friday:
		day = day - 2
	case time.Saturday:
		day = day - 3
	}

	// Wednesday 17:30
	now = time.Date(now.Year(), now.Month(), day, 17, 30, 0, 0, now.Location())
	ohs := openhours.NewSplitter(now)

	for _, test := range tests {
		t.Run(test.lstr, func(t *testing.T) {
			_, ok, err := ohs.Split(test.lstr)
			if err != nil {
				t.Fatal(err)
			}

			if ok != test.want {
				t.Errorf("split: case %q: got %v, want %v", test.lstr, ok, test.want)
			}

			ok, err = ohs.Match(test.lstr)
			if err != nil {
				t.Fatal(err)
			}

			if ok != test.want {
				t.Errorf("match: case %q: got %v, want %v", test.lstr, ok, test.want)
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
	now = time.Date(now.Year(), time.November, 13, 17, 30, 0, 0, now.Location())
	ohs := openhours.NewSplitter(now)

	for _, test := range tests {
		t.Run(test.lstr, func(t *testing.T) {
			_, _, err := ohs.Split(test.lstr)
			if err != nil {
				t.Fatal(err)
			}

			if ohs.String() != test.want {
				t.Errorf("case %q: got %v, want %v", test.lstr, ohs.String(), test.want)
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

	r := bufio.NewReader(bytes.NewReader(b))
	s := openhours.NewSplitter(time.Now())

	var (
		l        []byte
		sb       strings.Builder
		ok1, ok2 bool
	)

	for {
		l, _, err = r.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Errorf("read err %q: %v", string(l), err)
			continue
		}

		ok1, err = s.Match(string(l))
		if err != nil {
			t.Errorf("split err %q: %v", string(l), err)
		}

		_, ok2, err = s.Split(string(l))
		if err != nil {
			t.Errorf("split err %q: %v", string(l), err)
		}

		if ok1 != ok2 {
			t.Fatal("testdata: split.ok != match.ok")
		}

		sb.Reset()
		sb.WriteString(string(l))
		sb.WriteRune('\n')
		sb.WriteRune('=')
		sb.WriteRune('\n')
		sb.WriteString(s.String())
		sb.WriteRune('\n')
		fmt.Println(sb.String())
	}
}

func TestREADME(t *testing.T) {
	now := time.Now()
	fmt.Printf("%s\n\n", now.Format("Mon, 02 Jan 15:04"))

	ohs := openhours.NewSplitter(now)

	for _, v := range []string{
		"Mo-Tu, Fr 08:00-12:00 14:00-17:00 We 08:00-08:00 Th, Sa-Su 00:00-00:00",
		"Mo-Th 08:00-17:00; Fr 08:00-18:00; Sa 08:00-13:00",
	} {
		_, ok, err := ohs.Split(v)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s\n%s %v\n\n", v, ohs, ok)
	}
}

var blackhole bool

func BenchmarkSplit(b *testing.B) {
	now := time.Now()
	ohs := openhours.NewSplitter(now)
	var ok bool

	for i := 0; i < b.N; i++ {
		_, ok, _ = ohs.Split("Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30")
		blackhole = ok
	}
}

func BenchmarkMatch(b *testing.B) {
	now := time.Now()
	ohs := openhours.NewSplitter(now)
	var ok bool
	for i := 0; i < b.N; i++ {
		ok, _ = ohs.Match("Mo 09:00-19:00; Tu-Th, Sa-Su 10:00-19:00; Fr 09:00-17:30")
		blackhole = ok
	}
}
