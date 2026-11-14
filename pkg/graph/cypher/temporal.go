package cypher

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Temporal values: what bucketing a graph by time needs.
//
// Three value types join the plain ones in value.go while a query runs:
// Date (a calendar day), DateTime (an instant with a UTC offset) and Duration
// (months, days, seconds — the three parts openCypher keeps apart because a
// month is not a fixed number of days). A result row never carries them: on
// the way out each becomes its ISO 8601 string (externalize), so a row is
// still plain JSON and a date bucket reads "2026-03-01".
//
// Comparison follows openCypher. Two dates, or two datetimes, are ordered by
// when they are; a date is never equal to nor ordered against a datetime (the
// comparison is null — convert one side); two datetimes are equal only when
// they are the same instant at the same offset, so 10:00+08:00 and 02:00Z
// group apart and sort next to each other. Durations are equal part by part
// and are not ordered by < and >. ORDER BY places datetimes, then dates, then
// durations, after paths and before strings.
//
// Text that is not a date is an error, not null: date('soon') has no answer,
// and a null there would silently drop the row from every bucket.

// Date is a calendar date with no time and no zone.
type Date struct{ t time.Time } // midnight UTC

// DateTime is an instant with the UTC offset it was written in.
type DateTime struct{ t time.Time }

// Duration is an openCypher duration. Nanos is always in [0, 1e9).
type Duration struct {
	Months  int64
	Days    int64
	Seconds int64
	Nanos   int64
}

func (d Date) String() string { return d.t.Format("2006-01-02") }

// String is RFC 3339: seconds always present, the fraction only when there
// is one, Z for a zero offset.
func (d DateTime) String() string { return d.t.Format(time.RFC3339Nano) }

func (d Date) MarshalJSON() ([]byte, error)     { return []byte(strconv.Quote(d.String())), nil }
func (d DateTime) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(d.String())), nil }
func (d Duration) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(d.String())), nil }

// String is ISO 8601: P1Y2M3DT4H5M6.5S, with PT0S for the empty duration.
func (d Duration) String() string {
	var b strings.Builder
	b.WriteString("P")
	if y := d.Months / 12; y != 0 {
		fmt.Fprintf(&b, "%dY", y)
	}
	if m := d.Months % 12; m != 0 {
		fmt.Fprintf(&b, "%dM", m)
	}
	if d.Days != 0 {
		fmt.Fprintf(&b, "%dD", d.Days)
	}
	secs, nanos := d.Seconds, d.Nanos
	neg := false
	if secs < 0 && nanos > 0 {
		// -1.5s is stored as -2s + 0.5s; print it as written.
		secs, nanos, neg = secs+1, 1e9-nanos, true
	}
	h, rest := secs/3600, secs%3600
	mi, s := rest/60, rest%60
	if h != 0 || mi != 0 || s != 0 || nanos != 0 {
		b.WriteString("T")
		if h != 0 {
			fmt.Fprintf(&b, "%dH", h)
		}
		if mi != 0 {
			fmt.Fprintf(&b, "%dM", mi)
		}
		if s != 0 || nanos != 0 {
			if neg && s == 0 {
				b.WriteString("-")
			}
			b.WriteString(strconv.FormatInt(s, 10))
			if nanos != 0 {
				b.WriteString(".")
				b.WriteString(strings.TrimRight(fmt.Sprintf("%09d", nanos), "0"))
			}
			b.WriteString("S")
		}
	}
	if b.Len() == 1 {
		return "PT0S"
	}
	return b.String()
}

func isTemporal(v any) bool {
	switch v.(type) {
	case Date, DateTime, Duration:
		return true
	}
	return false
}

// externalize replaces temporal values with their ISO strings, copying a
// list or map only when something inside it changed: a list read straight
// from a node's properties is shared with that node.
func externalize(v any) any {
	switch x := v.(type) {
	case float64:
		// JSON has no NaN or infinity, and one such cell (log(0), sqrt(-1))
		// would fail the whole response: it leaves as null.
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
	case Date:
		return x.String()
	case DateTime:
		return x.String()
	case Duration:
		return x.String()
	case []any:
		var out []any
		for i, e := range x {
			n := externalize(e)
			if out == nil && isTemporalDeep(e) {
				out = make([]any, len(x))
				copy(out, x[:i])
			}
			if out != nil {
				out[i] = n
			}
		}
		if out != nil {
			return out
		}
	case map[string]any:
		if !isTemporalDeep(x) {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = externalize(e)
		}
		return out
	}
	return v
}

func isTemporalDeep(v any) bool {
	switch x := v.(type) {
	case Date, DateTime, Duration:
		return true
	case []any:
		for _, e := range x {
			if isTemporalDeep(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if isTemporalDeep(e) {
				return true
			}
		}
	}
	return false
}

// --- construction -----------------------------------------------------------

const minYear, maxYear = 0, 9999

func newDate(y, m, d int) (Date, bool) {
	if y < minYear || y > maxYear || m < 1 || m > 12 || d < 1 || d > 31 {
		return Date{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d { // 31 April, 30 February
		return Date{}, false
	}
	return Date{t}, true
}

func dateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

func inRange(t time.Time) bool { return t.Year() >= minYear && t.Year() <= maxYear }

func offsetZone(seconds int) *time.Location {
	if seconds == 0 {
		return time.UTC
	}
	return time.FixedZone("", seconds)
}

func offsetOf(t time.Time) int {
	_, off := t.Zone()
	return off
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// isoWeekStart is the Monday of ISO week w of week-year y. 4 January is
// always in week 1.
func isoWeekStart(y, w int) time.Time {
	jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC)
	return jan4.AddDate(0, 0, -(isoWeekday(jan4)-1)+(w-1)*7)
}

// isoWeekday is Monday = 1 … Sunday = 7.
func isoWeekday(t time.Time) int { return (int(t.Weekday())+6)%7 + 1 }

// parseDatePart reads the calendar forms: YYYY-MM-DD, YYYYMMDD, YYYY-MM,
// YYYY, the ISO week forms YYYY-Www and YYYY-Www-D, and the ordinal YYYY-DDD.
func parseDatePart(p string) (Date, bool) {
	if len(p) < 4 || !allDigits(p[:4]) {
		return Date{}, false
	}
	y := atoi(p[:4])
	rest := p[4:]
	switch {
	case rest == "":
		return newDate(y, 1, 1)
	case len(rest) == 6 && rest[0] == '-' && rest[3] == '-' && allDigits(rest[1:3]) && allDigits(rest[4:6]):
		return newDate(y, atoi(rest[1:3]), atoi(rest[4:6]))
	case len(rest) == 4 && allDigits(rest):
		return newDate(y, atoi(rest[:2]), atoi(rest[2:]))
	case len(rest) == 3 && rest[0] == '-' && allDigits(rest[1:]):
		return newDate(y, atoi(rest[1:]), 1)
	case len(rest) >= 4 && rest[:2] == "-W" && allDigits(rest[2:4]):
		w, dow := atoi(rest[2:4]), 1
		switch {
		case len(rest) == 4:
		case len(rest) == 6 && rest[4] == '-' && rest[5] >= '1' && rest[5] <= '7':
			dow = int(rest[5] - '0')
		default:
			return Date{}, false
		}
		start := isoWeekStart(y, w)
		if wy, ww := start.ISOWeek(); w < 1 || wy != y || ww != w { // week 53 of a 52-week year
			return Date{}, false
		}
		t := start.AddDate(0, 0, dow-1)
		if !inRange(t) {
			return Date{}, false
		}
		return Date{t}, true
	case len(rest) == 4 && rest[0] == '-' && allDigits(rest[1:]):
		n := atoi(rest[1:])
		t := time.Date(y, 1, n, 0, 0, 0, 0, time.UTC)
		if n < 1 || t.Year() != y || y > maxYear {
			return Date{}, false
		}
		return Date{t}, true
	}
	return Date{}, false
}

// parseZone reads Z, ±hh, ±hhmm or ±hh:mm.
func parseZone(z string) (*time.Location, bool) {
	if z == "Z" || z == "z" {
		return time.UTC, true
	}
	if len(z) < 3 || (z[0] != '+' && z[0] != '-') {
		return nil, false
	}
	body := z[1:]
	var hh, mm string
	switch {
	case len(body) == 2:
		hh, mm = body, "00"
	case len(body) == 4:
		hh, mm = body[:2], body[2:]
	case len(body) == 5 && body[2] == ':':
		hh, mm = body[:2], body[3:]
	default:
		return nil, false
	}
	if !allDigits(hh) || !allDigits(mm) || atoi(hh) > 18 || atoi(mm) > 59 {
		return nil, false
	}
	off := atoi(hh)*3600 + atoi(mm)*60
	if z[0] == '-' {
		off = -off
	}
	return offsetZone(off), true
}

// parseClock reads hh, hh:mm, hh:mm:ss, hhmm or hhmmss with an optional
// fraction of a second (. or , and up to nine digits).
func parseClock(c string) (h, m, s, ns int, ok bool) {
	frac := ""
	if i := strings.IndexAny(c, ".,"); i >= 0 {
		c, frac = c[:i], c[i+1:]
		if !allDigits(frac) || len(frac) > 9 {
			return 0, 0, 0, 0, false
		}
		ns = atoi(frac + strings.Repeat("0", 9-len(frac)))
	}
	var parts []string
	switch {
	case strings.Contains(c, ":"):
		parts = strings.Split(c, ":")
	case len(c) == 2 || len(c) == 4 || len(c) == 6:
		for i := 0; i < len(c); i += 2 {
			parts = append(parts, c[i:i+2])
		}
	}
	if len(parts) < 1 || len(parts) > 3 || (frac != "" && len(parts) != 3) {
		return 0, 0, 0, 0, false
	}
	var v [3]int
	for i, p := range parts {
		if len(p) != 2 || !allDigits(p) {
			return 0, 0, 0, 0, false
		}
		v[i] = atoi(p)
	}
	if v[0] > 23 || v[1] > 59 || v[2] > 59 {
		return 0, 0, 0, 0, false
	}
	return v[0], v[1], v[2], ns, true
}

// parseTemporalText reads a date, or a date and a time separated by T (or a
// space, which is how SQL writes a timestamp). A time with no offset is UTC.
// The time is returned in the offset it was written with.
func parseTemporalText(s string) (t time.Time, hasTime bool, problem string) {
	s = strings.TrimSpace(s)
	datePart, timePart := s, ""
	if i := strings.IndexAny(s, "Tt "); i >= 0 {
		datePart, timePart, hasTime = s[:i], s[i+1:], true
	}
	d, ok := parseDatePart(datePart)
	if !ok {
		return time.Time{}, false, "not a date"
	}
	if !hasTime {
		return d.t, false, ""
	}
	if strings.Contains(timePart, "[") {
		return time.Time{}, false, "named time zones are not supported; write a UTC offset such as +08:00"
	}
	clock, loc := timePart, time.UTC
	if i := strings.IndexAny(timePart, "Zz+-"); i >= 0 {
		clock = timePart[:i]
		if loc, ok = parseZone(timePart[i:]); !ok {
			return time.Time{}, false, "not a UTC offset"
		}
	}
	h, m, sec, ns, ok := parseClock(clock)
	if !ok {
		return time.Time{}, false, "not a time of day"
	}
	y, mo, day := d.t.Date()
	return time.Date(y, mo, day, h, m, sec, ns, loc), true, ""
}

func temporalParseErr(fn, s, problem string) error {
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return runtimeErr("%s() cannot read %q: %s (expected YYYY-MM-DD or an RFC 3339 timestamp such as 2026-03-05T10:00:00+08:00)", fn, s, problem)
}

func mapInts(fn string, m map[string]any, allowed ...string) (map[string]int64, error) {
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	out := map[string]int64{}
	for k, v := range m {
		if !ok[k] {
			return nil, runtimeErr("%s() does not take the field %s; fields are %s", fn, k, strings.Join(allowed, ", "))
		}
		if v == nil {
			return nil, runtimeErr("%s() field %s is null", fn, k)
		}
		if k == "timezone" {
			continue
		}
		n, isInt := v.(int64)
		if !isInt {
			return nil, runtimeErr("%s() field %s must be an integer, got a %s", fn, k, typeName(v))
		}
		out[k] = n
	}
	return out, nil
}

func dateFromMap(m map[string]any) (any, error) {
	f, err := mapInts("date", m, "year", "month", "day")
	if err != nil {
		return nil, err
	}
	y, has := f["year"]
	if !has {
		return nil, runtimeErr("date() needs a year")
	}
	mo, hasMonth := f["month"]
	if !hasMonth {
		mo = 1
	}
	day, hasDay := f["day"]
	if !hasDay {
		day = 1
	} else if !hasMonth {
		return nil, runtimeErr("date() takes a day only together with a month")
	}
	d, ok := newDate(int(clampInt(y)), int(clampInt(mo)), int(clampInt(day)))
	if !ok {
		return nil, runtimeErr("date(): %d-%d-%d is not a calendar date", y, mo, day)
	}
	return d, nil
}

// clampInt keeps an out-of-range field from wrapping when narrowed to int;
// the range check that follows refuses it.
func clampInt(n int64) int64 {
	const lim = 1 << 30
	return max(-lim, min(lim, n))
}

func datetimeFromMap(m map[string]any) (any, error) {
	f, err := mapInts("datetime", m, "year", "month", "day", "hour", "minute", "second",
		"millisecond", "microsecond", "nanosecond", "timezone", "epochSeconds", "epochMillis")
	if err != nil {
		return nil, err
	}
	loc := time.UTC
	if z, ok := m["timezone"]; ok {
		zs, isStr := z.(string)
		if !isStr {
			return nil, runtimeErr("datetime() field timezone must be a string offset such as '+08:00'")
		}
		if zs != "UTC" {
			if loc, ok = parseZone(zs); !ok {
				return nil, runtimeErr("datetime() timezone %q is not a UTC offset; named time zones are not supported", zs)
			}
		}
	}
	es, hasES := f["epochSeconds"]
	em, hasEM := f["epochMillis"]
	if hasES || hasEM {
		for k := range f {
			if k != "epochSeconds" && k != "epochMillis" && k != "nanosecond" {
				return nil, runtimeErr("datetime() takes epochSeconds or epochMillis with nanosecond and timezone only, not %s", k)
			}
		}
		if hasES && hasEM {
			return nil, runtimeErr("datetime() takes epochSeconds or epochMillis, not both")
		}
		return datetimeFromEpoch(es, em, hasEM, f["nanosecond"], loc)
	}
	y, has := f["year"]
	if !has {
		return nil, runtimeErr("datetime() needs a year, epochSeconds or epochMillis")
	}
	get := func(k string, def, lo, hi int64) (int, error) {
		v, ok := f[k]
		if !ok {
			return int(def), nil
		}
		if v < lo || v > hi {
			return 0, runtimeErr("datetime() field %s must be between %d and %d, got %d", k, lo, hi, v)
		}
		return int(v), nil
	}
	var parts [8]int
	for i, spec := range []struct {
		k           string
		def, lo, hi int64
	}{
		{"month", 1, 1, 12}, {"day", 1, 1, 31}, {"hour", 0, 0, 23}, {"minute", 0, 0, 59}, {"second", 0, 0, 59},
		{"millisecond", 0, 0, 999}, {"microsecond", 0, 0, 999_999}, {"nanosecond", 0, 0, 999_999_999},
	} {
		if parts[i], err = get(spec.k, spec.def, spec.lo, spec.hi); err != nil {
			return nil, err
		}
	}
	d, ok := newDate(int(clampInt(y)), parts[0], parts[1])
	if !ok {
		return nil, runtimeErr("datetime(): %d-%d-%d is not a calendar date", y, parts[0], parts[1])
	}
	ns := parts[5]*1_000_000 + parts[6]*1000 + parts[7]
	if ns > 999_999_999 {
		return nil, runtimeErr("datetime(): millisecond, microsecond and nanosecond add up to more than a second")
	}
	return DateTime{time.Date(d.t.Year(), d.t.Month(), d.t.Day(), parts[2], parts[3], parts[4], ns, loc)}, nil
}

func datetimeFromEpoch(secs, millis int64, useMillis bool, nanos int64, loc *time.Location) (any, error) {
	if nanos < 0 || nanos > 999_999_999 {
		return nil, runtimeErr("datetime() nanosecond must be between 0 and 999999999")
	}
	var t time.Time
	if useMillis {
		t = time.Unix(floorDiv(millis, 1000), floorMod(millis, 1000)*1_000_000+nanos)
	} else {
		t = time.Unix(secs, nanos)
	}
	t = t.In(loc)
	if !inRange(t) {
		return nil, runtimeErr("datetime(): the epoch value is outside the years 0000 to 9999")
	}
	return DateTime{t}, nil
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int64) int64 { return a - floorDiv(a, b)*b }

// --- functions --------------------------------------------------------------

// temporalFunctions are evaluated before the generic null-in, null-out rule
// of evalFunc: date() takes no argument at all, and the truncate functions
// take the unit first.
var temporalFunctions = map[string]func(ev *evaluator, args []any) (any, error){
	"date":                     fnDate,
	"datetime":                 fnDateTime,
	"duration":                 fnDuration,
	"date.truncate":            func(_ *evaluator, a []any) (any, error) { return fnTruncate("date.truncate", a) },
	"datetime.truncate":        func(_ *evaluator, a []any) (any, error) { return fnTruncate("datetime.truncate", a) },
	"datetime.fromepoch":       fnFromEpoch,
	"datetime.fromepochmillis": fnFromEpochMillis,
	"duration.between":         func(_ *evaluator, a []any) (any, error) { return fnDurationBetween("duration.between", a) },
	"duration.inmonths":        func(_ *evaluator, a []any) (any, error) { return fnDurationBetween("duration.inMonths", a) },
	"duration.indays":          func(_ *evaluator, a []any) (any, error) { return fnDurationBetween("duration.inDays", a) },
	"duration.inseconds":       func(_ *evaluator, a []any) (any, error) { return fnDurationBetween("duration.inSeconds", a) },
}

func (ev *evaluator) clock() time.Time {
	if ev.now.IsZero() {
		return time.Now().UTC()
	}
	return ev.now
}

func fnDate(ev *evaluator, args []any) (any, error) {
	if len(args) == 0 {
		return dateOf(ev.clock()), nil
	}
	switch x := args[0].(type) {
	case nil:
		return nil, nil
	case Date:
		return x, nil
	case DateTime:
		return dateOf(x.t), nil
	case string:
		t, _, problem := parseTemporalText(x)
		if problem != "" {
			return nil, temporalParseErr("date", x, problem)
		}
		return dateOf(t), nil
	case map[string]any:
		return dateFromMap(x)
	}
	return nil, runtimeErr("date() takes a string, a map or a datetime, got a %s", typeName(args[0]))
}

func fnDateTime(ev *evaluator, args []any) (any, error) {
	if len(args) == 0 {
		return DateTime{ev.clock()}, nil
	}
	switch x := args[0].(type) {
	case nil:
		return nil, nil
	case DateTime:
		return x, nil
	case Date:
		return DateTime{x.t}, nil
	case string:
		t, _, problem := parseTemporalText(x)
		if problem != "" {
			return nil, temporalParseErr("datetime", x, problem)
		}
		return DateTime{t}, nil
	case map[string]any:
		return datetimeFromMap(x)
	}
	return nil, runtimeErr("datetime() takes a string, a map or a date, got a %s (for epoch numbers use datetime({epochSeconds: n}) or datetime({epochMillis: n}))", typeName(args[0]))
}

func fnFromEpoch(_ *evaluator, args []any) (any, error) {
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	s, ok1 := args[0].(int64)
	n, ok2 := args[1].(int64)
	if !ok1 || !ok2 {
		return nil, runtimeErr("datetime.fromEpoch() takes integer seconds and nanoseconds")
	}
	return datetimeFromEpoch(s, 0, false, n, time.UTC)
}

func fnFromEpochMillis(_ *evaluator, args []any) (any, error) {
	if args[0] == nil {
		return nil, nil
	}
	ms, ok := args[0].(int64)
	if !ok {
		return nil, runtimeErr("datetime.fromEpochMillis() takes integer milliseconds, got a %s", typeName(args[0]))
	}
	return datetimeFromEpoch(0, ms, true, 0, time.UTC)
}

// truncateTime is the start of the unit t falls in, in t's own offset. The
// second result is false for a unit smaller than a day.
func truncateTime(unit string, t time.Time) (time.Time, bool, bool) {
	y, m, d := t.Date()
	loc := t.Location()
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, loc) }
	switch strings.ToLower(unit) {
	case "millennium":
		return day(y-y%1000, 1, 1), true, true
	case "century":
		return day(y-y%100, 1, 1), true, true
	case "decade":
		return day(y-y%10, 1, 1), true, true
	case "year":
		return day(y, 1, 1), true, true
	case "weekyear":
		wy, _ := t.ISOWeek()
		s := isoWeekStart(wy, 1)
		return day(s.Year(), s.Month(), s.Day()), true, true
	case "quarter":
		return day(y, (m-1)/3*3+1, 1), true, true
	case "month":
		return day(y, m, 1), true, true
	case "week":
		return day(y, m, d-(isoWeekday(t)-1)), true, true
	case "day":
		return day(y, m, d), true, true
	case "hour":
		return time.Date(y, m, d, t.Hour(), 0, 0, 0, loc), false, true
	case "minute":
		return time.Date(y, m, d, t.Hour(), t.Minute(), 0, 0, loc), false, true
	case "second":
		return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, loc), false, true
	case "millisecond":
		return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1_000_000*1_000_000, loc), false, true
	case "microsecond":
		return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1000*1000, loc), false, true
	}
	return time.Time{}, false, false
}

const (
	dateUnits     = "millennium, century, decade, year, weekYear, quarter, month, week, day"
	datetimeUnits = dateUnits + ", hour, minute, second, millisecond, microsecond"
)

func fnTruncate(fn string, args []any) (any, error) {
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	unit, ok := args[0].(string)
	if !ok {
		return nil, runtimeErr("%s() takes the unit first, as a string: %s('month', d)", fn, fn)
	}
	var t time.Time
	switch x := args[1].(type) {
	case Date:
		t = x.t
	case DateTime:
		t = x.t
	case string:
		return nil, runtimeErr("%s() takes a date or datetime, got a string; convert it first: %s('%s', date(x))", fn, fn, unit)
	default:
		return nil, runtimeErr("%s() takes a date or datetime, got a %s", fn, typeName(args[1]))
	}
	out, wholeDay, known := truncateTime(unit, t)
	isDate := fn == "date.truncate"
	if !known || (isDate && !wholeDay) {
		units := datetimeUnits
		if isDate {
			units = dateUnits
		}
		return nil, runtimeErr("%s() does not take the unit %q; units are %s", fn, unit, units)
	}
	if !inRange(out) {
		return nil, runtimeErr("%s(): the result is outside the years 0000 to 9999", fn)
	}
	if isDate {
		return dateOf(out), nil
	}
	return DateTime{out}, nil
}

// --- components -------------------------------------------------------------

// temporalComponent is d.year, dt.hour, dur.days and the rest. An unknown
// component is an error: it is a misspelling, not a missing property.
func temporalComponent(v any, key string) (any, error) {
	switch x := v.(type) {
	case Date:
		if c, ok := dateComponent(x.t, key); ok {
			return c, nil
		}
		return nil, runtimeErr("a date has no component %s; components are year, quarter, month, week, weekYear, day, ordinalDay, dayOfWeek, dayOfQuarter", key)
	case DateTime:
		if c, ok := dateComponent(x.t, key); ok {
			return c, nil
		}
		t := x.t
		off := offsetOf(t)
		switch key {
		case "hour":
			return int64(t.Hour()), nil
		case "minute":
			return int64(t.Minute()), nil
		case "second":
			return int64(t.Second()), nil
		case "millisecond":
			return int64(t.Nanosecond() / 1_000_000), nil
		case "microsecond":
			return int64(t.Nanosecond() / 1000), nil
		case "nanosecond":
			return int64(t.Nanosecond()), nil
		case "timezone", "offset":
			return t.Format("Z07:00"), nil
		case "offsetMinutes":
			return int64(off / 60), nil
		case "offsetSeconds":
			return int64(off), nil
		case "epochSeconds":
			return t.Unix(), nil
		case "epochMillis":
			return t.UnixMilli(), nil
		}
		return nil, runtimeErr("a datetime has no component %s; components are year, quarter, month, week, weekYear, day, ordinalDay, dayOfWeek, dayOfQuarter, hour, minute, second, millisecond, microsecond, nanosecond, timezone, offset, offsetMinutes, offsetSeconds, epochSeconds, epochMillis", key)
	case Duration:
		switch key {
		case "years":
			return x.Months / 12, nil
		case "quarters":
			return x.Months / 3, nil
		case "months":
			return x.Months, nil
		case "weeks":
			return x.Days / 7, nil
		case "days":
			return x.Days, nil
		case "hours":
			return x.Seconds / 3600, nil
		case "minutes":
			return x.Seconds / 60, nil
		case "seconds":
			return x.Seconds, nil
		case "milliseconds":
			return x.Seconds*1000 + x.Nanos/1_000_000, nil
		case "microseconds":
			return x.Seconds*1_000_000 + x.Nanos/1000, nil
		case "nanoseconds":
			return x.Seconds*1_000_000_000 + x.Nanos, nil
		case "monthsOfYear":
			return x.Months % 12, nil
		case "daysOfWeek":
			return x.Days % 7, nil
		case "minutesOfHour":
			return x.Seconds / 60 % 60, nil
		case "secondsOfMinute":
			return x.Seconds % 60, nil
		case "millisecondsOfSecond":
			return x.Nanos / 1_000_000, nil
		case "microsecondsOfSecond":
			return x.Nanos / 1000, nil
		case "nanosecondsOfSecond":
			return x.Nanos, nil
		}
		return nil, runtimeErr("a duration has no component %s; components are years, quarters, months, weeks, days, hours, minutes, seconds, milliseconds, microseconds, nanoseconds, monthsOfYear, daysOfWeek, minutesOfHour, secondsOfMinute, millisecondsOfSecond, microsecondsOfSecond, nanosecondsOfSecond", key)
	}
	return nil, runtimeErr("cannot read property %s of a %s", key, typeName(v))
}

func dateComponent(t time.Time, key string) (any, bool) {
	switch key {
	case "year":
		return int64(t.Year()), true
	case "quarter":
		return int64((t.Month()-1)/3 + 1), true
	case "month":
		return int64(t.Month()), true
	case "week":
		_, w := t.ISOWeek()
		return int64(w), true
	case "weekYear":
		y, _ := t.ISOWeek()
		return int64(y), true
	case "day":
		return int64(t.Day()), true
	case "ordinalDay":
		return int64(t.YearDay()), true
	case "dayOfWeek", "weekDay":
		return int64(isoWeekday(t)), true
	case "dayOfQuarter", "quarterDay":
		q := time.Date(t.Year(), (t.Month()-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC)
		return int64(t.YearDay() - q.YearDay() + 1), true
	}
	return nil, false
}

// --- durations --------------------------------------------------------------

func newDuration(months, days, seconds, nanos int64) Duration {
	seconds += floorDiv(nanos, 1_000_000_000)
	return Duration{Months: months, Days: days, Seconds: seconds, Nanos: floorMod(nanos, 1_000_000_000)}
}

func (d Duration) negate() Duration { return newDuration(-d.Months, -d.Days, -d.Seconds, -d.Nanos) }

var durationText = regexp.MustCompile(`^P(?:(-?\d+)Y)?(?:(-?\d+)M)?(?:(-?\d+)W)?(?:(-?\d+)D)?(?:T(?:(-?\d+)H)?(?:(-?\d+)M)?(?:(-?\d+)(?:[.,](\d{1,9}))?S)?)?$`)

func parseDurationText(s string) (Duration, bool) {
	m := durationText.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Duration{}, false
	}
	var n [7]int64
	found := false
	for i := 0; i < 7; i++ {
		if m[i+1] == "" {
			continue
		}
		v, err := strconv.ParseInt(m[i+1], 10, 64)
		if err != nil || v > 1<<40 || v < -(1<<40) {
			return Duration{}, false
		}
		n[i], found = v, true
	}
	if !found {
		return Duration{}, false
	}
	var nanos int64
	if m[8] != "" {
		nanos = int64(atoi(m[8] + strings.Repeat("0", 9-len(m[8]))))
		if strings.HasPrefix(m[7], "-") {
			nanos = -nanos
		}
	}
	return newDuration(n[0]*12+n[1], n[2]*7+n[3], n[4]*3600+n[5]*60+n[6], nanos), true
}

func fnDuration(_ *evaluator, args []any) (any, error) {
	switch x := args[0].(type) {
	case nil:
		return nil, nil
	case Duration:
		return x, nil
	case string:
		d, ok := parseDurationText(x)
		if !ok {
			return nil, runtimeErr("duration() cannot read %q (expected ISO 8601 such as P1M, P7D or PT12H30M)", x)
		}
		return d, nil
	case map[string]any:
		f, err := mapInts("duration", x, "years", "quarters", "months", "weeks", "days", "hours", "minutes", "seconds", "milliseconds", "microseconds", "nanoseconds")
		if err != nil {
			return nil, err
		}
		for k, v := range f {
			if v > 1<<40 || v < -(1<<40) {
				return nil, runtimeErr("duration() field %s is out of range", k)
			}
		}
		return newDuration(
			f["years"]*12+f["quarters"]*3+f["months"],
			f["weeks"]*7+f["days"],
			f["hours"]*3600+f["minutes"]*60+f["seconds"],
			f["milliseconds"]*1_000_000+f["microseconds"]*1000+f["nanoseconds"],
		), nil
	}
	return nil, runtimeErr("duration() takes an ISO 8601 string or a map, got a %s", typeName(args[0]))
}

// addMonths moves t by whole months, keeping the day of the month where the
// target month has it and its last day where it does not (31 Jan + 1 month
// is 28 or 29 Feb).
func addMonths(t time.Time, n int64) time.Time {
	y, m, d := t.Date()
	total := int64(y)*12 + int64(m-1) + n
	ny, nm := int(floorDiv(total, 12)), time.Month(floorMod(total, 12)+1)
	if last := time.Date(ny, nm+1, 0, 0, 0, 0, 0, time.UTC).Day(); d > last {
		d = last
	}
	return time.Date(ny, nm, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

const maxShift = 12 * 20000 // months; beyond this nothing stays in range

func shift(t time.Time, d Duration, wholeDays bool) (time.Time, bool) {
	if d.Months > maxShift || d.Months < -maxShift || d.Days > maxShift*31 || d.Days < -maxShift*31 {
		return time.Time{}, false
	}
	t = addMonths(t, d.Months).AddDate(0, 0, int(d.Days))
	if !wholeDays {
		if d.Seconds > maxShift*31*86400 || d.Seconds < -maxShift*31*86400 {
			return time.Time{}, false
		}
		t = time.Unix(t.Unix()+d.Seconds, int64(t.Nanosecond())+d.Nanos).In(t.Location())
	}
	return t, inRange(t)
}

// temporalArith is + and - over temporal values. handled is false when
// neither operand is one.
func temporalArith(op string, a, b any) (v any, handled bool, err error) {
	if !isTemporal(a) && !isTemporal(b) {
		return nil, false, nil
	}
	da, aDur := a.(Duration)
	db, bDur := b.(Duration)
	outOfRange := runtimeErr("the result of %s is outside the years 0000 to 9999", op)
	switch {
	case aDur && bDur && (op == "+" || op == "-"):
		if op == "-" {
			db = db.negate()
		}
		return newDuration(da.Months+db.Months, da.Days+db.Days, da.Seconds+db.Seconds, da.Nanos+db.Nanos), true, nil
	case bDur && (op == "+" || op == "-"):
		if op == "-" {
			db = db.negate()
		}
		switch x := a.(type) {
		case Date:
			// A date has no time of day: the sub-day part of the duration
			// does not move it.
			t, ok := shift(x.t, db, true)
			if !ok {
				return nil, true, outOfRange
			}
			return Date{t}, true, nil
		case DateTime:
			t, ok := shift(x.t, db, false)
			if !ok {
				return nil, true, outOfRange
			}
			return DateTime{t}, true, nil
		}
	case aDur && op == "+":
		return temporalArith(op, b, a)
	}
	if op == "-" && !aDur && !bDur && isTemporal(a) && isTemporal(b) {
		return nil, true, runtimeErr("cannot subtract a %s from a %s; use duration.between(a, b), duration.inDays(a, b) or duration.inSeconds(a, b)", typeName(b), typeName(a))
	}
	return nil, true, runtimeErr("operator %s cannot take a %s and a %s", op, typeName(a), typeName(b))
}

// calendarEnd is b's date as monthsUntil and daysUntil count towards it: whole
// months, or whole days, from a to b the way a calendar does. A unit is whole
// once the same time of day comes round, so the day b falls on does not count
// until then; and a month is whole once the same day of the month does, so
// 31 Jan → 28 Feb is 0 months and 28 days.
func calendarEnd(a, b time.Time) time.Time {
	end := dateOf(b).t
	start := dateOf(a).t
	ta := a.Sub(time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, a.Location()))
	tb := b.Sub(time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, b.Location()))
	switch {
	case end.After(start) && tb < ta:
		end = end.AddDate(0, 0, -1)
	case end.Before(start) && tb > ta:
		end = end.AddDate(0, 0, 1)
	}
	return end
}

func monthsUntil(a, b time.Time) int64 {
	end := calendarEnd(a, b)
	pack := func(t time.Time) int64 { return (int64(t.Year())*12+int64(t.Month()-1))*32 + int64(t.Day()) }
	return (pack(end) - pack(a)) / 32
}

func daysUntil(a, b time.Time) int64 {
	end := calendarEnd(a, b)
	return floorDiv(end.Unix()-dateOf(a).t.Unix(), 86400)
}

func fnDurationBetween(fn string, args []any) (any, error) {
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	var ts [2]time.Time
	var isDate [2]bool
	for i, v := range args {
		switch x := v.(type) {
		case Date:
			ts[i], isDate[i] = x.t, true
		case DateTime:
			ts[i] = x.t
		default:
			return nil, runtimeErr("%s() takes two dates or datetimes, got a %s; convert with date(x) or datetime(x)", fn, typeName(v))
		}
	}
	// Both sides are read on one clock: a date is midnight in the other
	// side's offset, and the second datetime is read in the first's.
	a, b := ts[0], ts[1]
	switch {
	case isDate[0] && !isDate[1]:
		a = time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, b.Location())
	case !isDate[0] && isDate[1]:
		b = time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, a.Location())
	default:
		b = b.In(a.Location())
	}
	switch fn {
	case "duration.inMonths":
		return newDuration(monthsUntil(a, b), 0, 0, 0), nil
	case "duration.inDays":
		return newDuration(0, daysUntil(a, b), 0, 0), nil
	case "duration.inSeconds":
		return newDuration(0, 0, b.Unix()-a.Unix(), int64(b.Nanosecond()-a.Nanosecond())), nil
	}
	months := monthsUntil(a, b)
	mid := addMonths(a, months)
	days := daysUntil(mid, b)
	mid = mid.AddDate(0, 0, int(days))
	return newDuration(months, days, b.Unix()-mid.Unix(), int64(b.Nanosecond()-mid.Nanosecond())), nil
}

// durationOrder is a length to sort durations by: a month counts as the
// average Gregorian month. It orders ORDER BY, min and max; < and > do not
// use it.
func durationOrder(d Duration) float64 {
	const monthSeconds = 2_629_746
	return float64(d.Months)*monthSeconds + float64(d.Days)*86400 + float64(d.Seconds) + float64(d.Nanos)/1e9
}

// storedInstant reads a created_at / updated_at column — a SQL timestamp in
// whichever spelling the driver hands back — as an RFC 3339 UTC string to the
// second, so text order is time order. Empty or unreadable is null.
func storedInstant(raw string) any {
	if raw == "" {
		return nil
	}
	t, _, problem := parseTemporalText(raw)
	if problem != "" {
		// time.Time's own String(), which a driver may fall back to.
		var err error
		if t, err = time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", raw); err != nil {
			return nil
		}
	}
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}
