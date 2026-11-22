package cortexdb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Event time: when a relation was true in the world, as its source says (#252).
//
// The graph's valid_from/valid_to columns record when the store held a row,
// and a live row never has a valid_to, so a fact that ended ("worked at
// Oracle until 2008") cannot be told there without leaving every current
// query. Event time is kept where graphflow's temporal facts keep it: the
// relation edge's properties, valid_from and valid_to as RFC 3339, the half
// open interval [valid_from, valid_to). SaveTemporalFact, QueryFactsAsOf,
// extraction and recall all read and write those two keys.
const (
	// EventValidFromKey is the edge property holding when a relation began
	// to be true, RFC 3339 UTC.
	EventValidFromKey = "valid_from"
	// EventValidToKey is the edge property holding when it stopped, RFC 3339
	// UTC, exclusive. Absent: still true, or the source does not say.
	EventValidToKey = "valid_to"
)

// parseEventTime reads a time a source or caller states: RFC 3339, a date
// (2006-01-02), a year-month (2006-01) or a year (2006). The second result
// is the end of the period it names, exclusive: a year runs to the next
// January, a date to the next day.
func parseEventTime(value string) (time.Time, time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		t = t.UTC()
		return t, t, nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, t.AddDate(0, 0, 1), nil
	}
	if t, err := time.Parse("2006-01", v); err == nil {
		return t, t.AddDate(0, 1, 0), nil
	}
	if len(v) == 4 {
		if y, err := strconv.Atoi(v); err == nil && y > 0 {
			t := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
			return t, t.AddDate(1, 0, 0), nil
		}
	}
	return time.Time{}, time.Time{}, fmt.Errorf("time %q is not RFC 3339, YYYY-MM-DD, YYYY-MM or YYYY", value)
}

// eventInterval resolves what a relation says of its time. at names the
// period it happened in, valid_from and valid_to its bounds; at is the
// shorthand for both, so it cannot be given with either.
func eventInterval(validFrom, validTo, at string) (from, to time.Time, err error) {
	if strings.TrimSpace(at) != "" {
		if strings.TrimSpace(validFrom) != "" || strings.TrimSpace(validTo) != "" {
			return from, to, fmt.Errorf("at names the relation's period; give it or valid_from/valid_to, not both")
		}
		return parseEventTime(at)
	}
	if strings.TrimSpace(validFrom) != "" {
		if from, _, err = parseEventTime(validFrom); err != nil {
			return from, to, fmt.Errorf("valid_from: %w", err)
		}
	}
	if strings.TrimSpace(validTo) != "" {
		// "until 2008" ends when 2008 begins: a bound is the start of the
		// period it names.
		if to, _, err = parseEventTime(validTo); err != nil {
			return from, to, fmt.Errorf("valid_to: %w", err)
		}
	}
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		return from, to, fmt.Errorf("valid_to %s is not after valid_from %s", validTo, validFrom)
	}
	return from, to, nil
}

// setEventTime writes an interval into edge properties; zero bounds are left
// out.
func setEventTime(props map[string]any, from, to time.Time) {
	if !from.IsZero() {
		props[EventValidFromKey] = from.UTC().Format(time.RFC3339)
	}
	if !to.IsZero() {
		props[EventValidToKey] = to.UTC().Format(time.RFC3339)
	}
}

// eventTimeOf reads an edge's event interval back from its properties.
func eventTimeOf(props map[string]any) (from, to time.Time) {
	read := func(key string) time.Time {
		s, _ := props[key].(string)
		t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
		if err != nil {
			return time.Time{}
		}
		return t.UTC()
	}
	return read(EventValidFromKey), read(EventValidToKey)
}

var (
	isoDateRe   = regexp.MustCompile(`\b(1[89]\d\d|20\d\d)-(0[1-9]|1[0-2])(?:-(0[1-9]|[12]\d|3[01]))?\b`)
	monthYearRe = regexp.MustCompile(`(?i)\b(January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)\.?\s+(1[89]\d\d|20\d\d)\b`)
	// A range of years: "2005-2008", "2005 to 2008".
	rangeRe = regexp.MustCompile(`\b(1[89]\d\d|20\d\d)\s*(?:-|–|—|to|until|till)\s*(1[89]\d\d|20\d\d)\b`)
	// "until 2008" bounds the relation's end, not its start.
	untilYearRe = regexp.MustCompile(`(?i)\b(?:until|till)\s+(1[89]\d\d|20\d\d)\b`)
	// A year standing alone, after a word that introduces a time: "in 2006",
	// "since 2006", "by 2006". A bare number elsewhere may be a quantity
	// ("2006 engineers").
	yearRe = regexp.MustCompile(`(?i)\b(?:in|since|from|by|during|around|circa|early|late|mid|year|of)\s+(1[89]\d\d|20\d\d)\b`)
)

var monthNumber = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8,
	"sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
}

// statedEventTime is the time a chunk's text gives a relation: from the
// sentences naming both its ends, the one time they state, the range they
// state ("2005-2008", "from 2005 until 2008"), or an end ("until 2008").
// Nothing when no sentence names both, or when they state more than one
// time, since the text does not then say which belongs to the relation.
func statedEventTime(text, fromName, toName string) (from, to time.Time, ok bool) {
	a, b := strings.ToLower(strings.TrimSpace(fromName)), strings.ToLower(strings.TrimSpace(toName))
	if a == "" || b == "" {
		return from, to, false
	}
	type period struct{ from, to time.Time }
	var found []period
	for _, s := range splitSentences(text) {
		low := strings.ToLower(s)
		if !containsWord(low, a) || !containsWord(low, b) {
			continue
		}
		rest := s
		for _, m := range rangeRe.FindAllStringSubmatch(rest, -1) {
			f, _, _ := parseEventTime(m[1])
			t, _, _ := parseEventTime(m[2])
			if t.After(f) {
				found = append(found, period{f, t})
			}
		}
		rest = rangeRe.ReplaceAllString(rest, " ")
		for _, m := range isoDateRe.FindAllString(rest, -1) {
			if f, _, err := parseEventTime(m); err == nil {
				found = append(found, period{from: f})
			}
		}
		rest = isoDateRe.ReplaceAllString(rest, " ")
		for _, m := range monthYearRe.FindAllStringSubmatch(rest, -1) {
			key := strings.ToLower(m[1])
			if key != "sept" {
				key = key[:3]
			}
			y, _ := strconv.Atoi(m[2])
			found = append(found, period{from: time.Date(y, time.Month(monthNumber[key]), 1, 0, 0, 0, 0, time.UTC)})
		}
		rest = monthYearRe.ReplaceAllString(rest, " ")
		for _, m := range untilYearRe.FindAllStringSubmatch(rest, -1) {
			t, _, _ := parseEventTime(m[1])
			found = append(found, period{to: t})
		}
		rest = untilYearRe.ReplaceAllString(rest, " ")
		for _, m := range yearRe.FindAllStringSubmatch(rest, -1) {
			f, _, _ := parseEventTime(m[1])
			found = append(found, period{from: f})
		}
	}
	if len(found) == 0 {
		return from, to, false
	}
	// "from 2005 until 2008" is a start and an end; anything more is more
	// than one time.
	var p period
	for _, f := range found {
		switch {
		case !f.from.IsZero() && !f.to.IsZero():
			if !p.from.IsZero() || !p.to.IsZero() {
				return from, to, false
			}
			p = f
		case !f.from.IsZero():
			if !p.from.IsZero() && !p.from.Equal(f.from) {
				return from, to, false
			}
			p.from = f.from
		default:
			if !p.to.IsZero() && !p.to.Equal(f.to) {
				return from, to, false
			}
			p.to = f.to
		}
	}
	if !p.from.IsZero() && !p.to.IsZero() && !p.to.After(p.from) {
		return from, to, false
	}
	return p.from, p.to, true
}

// splitSentences cuts text at sentence ends: a period, question or
// exclamation mark followed by space, or a line break.
func splitSentences(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		end := c == '\n'
		if (c == '.' || c == '?' || c == '!') && (i+1 == len(text) || text[i+1] == ' ' || text[i+1] == '\n') {
			end = true
		}
		if end {
			if s := strings.TrimSpace(text[start : i+1]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// relationEventTime is the event interval a relation is written with: the
// one its caller or extractor states (valid_from, valid_to, at), else the
// one the text states in a sentence naming both its ends. A co-occurrence
// has no time of its own, and a relation whose metadata already carries one
// (graphflow's temporal facts) keeps it.
func relationEventTime(validFrom, validTo, at, from, to, relType, text string, metadata map[string]string) (time.Time, time.Time, error) {
	f, t, err := eventInterval(validFrom, validTo, at)
	if err != nil || !f.IsZero() || !t.IsZero() {
		return f, t, err
	}
	if metadata[EventValidFromKey] != "" || metadata[EventValidToKey] != "" || isWeakPredicate(relType) || strings.TrimSpace(text) == "" {
		return f, t, nil
	}
	if strings.HasPrefix(from, EntityNodeIDPrefix) || strings.HasPrefix(to, EntityNodeIDPrefix) {
		return f, t, nil
	}
	if sf, st, ok := statedEventTime(text, from, to); ok {
		return sf, st, nil
	}
	return f, t, nil
}

// EventTimeProperties is the edge properties a relation's stated time is
// written as (valid_from, valid_to, RFC 3339), for a sibling package that
// writes relation edges itself: at names the period, or valid_from and
// valid_to its bounds, each RFC 3339, YYYY-MM-DD, YYYY-MM or YYYY. Empty
// input gives no properties.
func EventTimeProperties(validFrom, validTo, at string) (map[string]any, error) {
	from, to, err := eventInterval(validFrom, validTo, at)
	if err != nil {
		return nil, err
	}
	props := map[string]any{}
	setEventTime(props, from, to)
	return props, nil
}
