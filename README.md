# openhours

Go parser and evaluator for [OpenStreetMap `opening_hours`](https://wiki.openstreetmap.org/wiki/Key:opening_hours) strings.

## Grammar

A deliberately small, strictly validated subset of the OSM syntax:

- **Days**: two-letter abbreviations `Mo Tu We Th Fr Sa Su` (case-insensitive),
  lists (`Mo, We, Fr`) and ranges (`Mo-Fr`); ranges may wrap the week end
  (`Fr-Mo` = Fr, Sa, Su, Mo).
- **Times**: `H:MM` or `HH:MM`, hour ≤ 24, minutes ≤ 59; `24:00` (and `00:00`)
  as a close time mean end of day; a close equal to or earlier than its open
  spans midnight (`Su 22:00-02:00`; `We 08:00-08:00` is a full 24 hours, per
  OSM semantics).
- **Rules**: a day group followed by one or more `open-close` intervals
  (`Mo-Fr 08:00-13:00 14:00-18:00`); several groups follow each other,
  separated by whitespace, `,` or `;`. A trailing day group without times
  means open the whole day. `24/7` (alone or with a rule tail) is an alias
  for `Mo-Su`.

**Strict where it matters, lenient where it doesn't.** Malformed *time syntax
and structure* fail `Parse` with `ErrInvalidSchedule` (wrapped with the reason
and byte offset) instead of silently producing a plausible-but-wrong schedule —
the worst failure mode for an "open now" filter: out-of-range clock values,
missing `:`, an open time without its close, two times without `-`. Unknown
*words* (`PH off`, localized labels) are transparent noise, so real-world
feeds still parse. Not supported: month/date ranges, week numbers, public
holidays, sunrise/sunset.

Validate at ingest with `Parse`; the package-level `Match` never returns an
error — an unparsable spec simply never matches.

## Usage

### Batch matching (hot path)

Package-level `Match` uses a built-in LRU cache, so repeated calls with the same spec string do not re-parse it. Cache hits are lock-free and scale linearly across goroutines; a miss pays a one-time snapshot rebuild (O(cache size)) per new spec. Suitable for filtering large lists.

```go
now := time.Now()

for _, p := range pharmacies {
    if openhours.Match(p.Schedule, now) {
        results = append(results, p)
    }
}
```

For the fastest path, pre-parse schedules at ingest (see below) and decompose
the instant once per request with `TimeOf`: `MatchAt` then costs only the
interval scan — about an order of magnitude cheaper per point than `Match`,
with no shared state between goroutines.

```go
wt := openhours.TimeOf(time.Now()) // once per request

for _, p := range pharmacies {
    if p.Schedule.MatchAt(wt) { // p.Schedule is a pre-parsed openhours.Schedule
        results = append(results, p)
    }
}
```

### Pre-parsed schedule

`Parse` compiles a spec string once. Use it when the set of schedules is fixed (loaded from config or DB at startup) or when you need more than just a boolean.

```go
l, err := openhours.Parse("Mo-Fr 09:00-20:00; Sa 10:00-18:00")
if err != nil {
    // spec is malformed or contains no recognisable schedule entries
}

// Is it open right now?
open := l.Match(now)

// When does the current open/closed period end?
open, boundary := l.Until(now)
if open {
    fmt.Printf("closes at %s\n", boundary.Format("15:04"))
} else {
    fmt.Printf("opens at %s\n", boundary.Format("15:04"))
}

// All open/close boundaries for the current week.
boundaries, open := l.Split(now)

// Human-readable weekly schedule; current interval is marked with '*'.
fmt.Println(l.Format(now))
```

### Example output

```go
now := time.Date(2022, time.November, 13, 21, 32, 0, 0, time.Local) // Sunday

for _, v := range []string{
    "Mo-Tu, Fr 08:00-12:00 14:00-17:00 We 08:00-08:00 Th, Sa-Su 00:00-00:00",
    "Mo-Th 08:00-17:00; Fr 08:00-18:00; Sa 08:00-13:00",
} {
    l, _ := openhours.Parse(v)
    fmt.Printf("%s\n%s %v\n\n", v, l.Format(now), l.Match(now))
}
```

```
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
```

## See also

- https://pkg.go.dev/github.com/chneau/openhours
- https://pkg.go.dev/github.com/yauhen-l/openhours
