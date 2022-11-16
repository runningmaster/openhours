# openhours

openhours is a simple Go parser/splitter/matcher for ["opening_hours"](https://wiki.openstreetmap.org/wiki/Key:opening_hours).

## Example

```go
now := time.Now()
fmt.Printf("%s\n\n", now.Format("Mon, 02 Jan 15:04"))

ohs := openhours.NewSplitter(now)

for _, v := range []string{
	"Mo-Tu, Fr 08:00-12:00 14:00-17:00 We 08:00-08:00 Th, Sa-Su 00:00-00:00",
	"Mo-Th 08:00-17:00; Fr 08:00-18:00; Sa 08:00-13:00",
} {
	_, ok, err := ohs.Split(v) // ok, err := ohs.Match(v)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n%s %v\n\n", v, ohs, ok)
}

/* output
Sun, 13 Nov 21:32

Mo-Tu, Fr 08:00-12:00 14:00-17:00 We 08:00-08:00 Th, Sa-Su 00:00-00:00
Mon, 07 Nov 08:00-12:00 14:00-17:00
Tue, 08 Nov 08:00-12:00 14:00-17:00
Wed, 09 Nov 08:00-08:00
Thu, 10 Nov 00:00-23:59
Fri, 11 Nov 08:00-12:00 14:00-17:00
Sat, 12 Nov 00:00-23:59
Sun, 13 Nov 00:00*23:59 true

Mo-Th 08:00-17:00; Fr 08:00-18:00; Sa 08:00-13:00
Mon, 07 Nov 08:00-17:00
Tue, 08 Nov 08:00-17:00
Wed, 09 Nov 08:00-17:00
Thu, 10 Nov 08:00-17:00
Fri, 11 Nov 08:00-18:00
Sat, 12 Nov 08:00-13:00 false
*/
```

### See also:

* https://pkg.go.dev/github.com/chneau/openhours
* https://pkg.go.dev/github.com/yauhen-l/openhours
