package cortexdb

import (
	"testing"
	"time"
)

func TestStatedEventTime(t *testing.T) {
	y := func(year int) time.Time { return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC) }
	for _, tc := range []struct {
		text     string
		from, to time.Time
		ok       bool
	}{
		{"In 2006 Sal Khan joined Oracle as a product manager.", y(2006), time.Time{}, true},
		{"Sal Khan worked at Oracle from 2005 to 2008.", y(2005), y(2008), true},
		{"Sal Khan worked at Oracle 2005–2008, then left.", y(2005), y(2008), true},
		{"Sal Khan stayed at Oracle until 2008.", time.Time{}, y(2008), true},
		{"In February 2009 Sal Khan left Oracle.", time.Date(2009, 2, 1, 0, 0, 0, 0, time.UTC), time.Time{}, true},
		{"Sal Khan joined Oracle on 2006-03-15.", time.Date(2006, 3, 15, 0, 0, 0, 0, time.UTC), time.Time{}, true},
		// Two times: the sentence does not say which is the relation's.
		{"Sal Khan, born 1976, joined Oracle in 2006.", y(2006), time.Time{}, true},
		{"Sal Khan joined Oracle in 2006 and left Oracle in 2009.", time.Time{}, time.Time{}, false},
		{"Sal Khan was at Oracle from 2005 until 2008.", y(2005), y(2008), true},
		// The time is in another sentence.
		{"It was 2006. Sal Khan joined Oracle.", time.Time{}, time.Time{}, false},
		// Not a year: a quantity, a version.
		{"Sal Khan said Oracle had 2006 engineers on v2006.1.", time.Time{}, time.Time{}, false},
		{"Sal Khan joined Oracle.", time.Time{}, time.Time{}, false},
	} {
		from, to, ok := statedEventTime(tc.text, "Sal Khan", "Oracle")
		if ok != tc.ok || !from.Equal(tc.from) || !to.Equal(tc.to) {
			t.Errorf("%q: got %v %v %v, want %v %v %v", tc.text, from, to, ok, tc.from, tc.to, tc.ok)
		}
	}
}

func TestEventInterval(t *testing.T) {
	from, to, err := eventInterval("", "", "2006")
	if err != nil || from.Year() != 2006 || to.Year() != 2007 {
		t.Fatalf("at 2006: %v %v %v", from, to, err)
	}
	if _, _, err := eventInterval("2008", "2005", ""); err == nil {
		t.Fatal("an interval ending before it starts was accepted")
	}
	if _, _, err := eventInterval("2005", "", "2006"); err == nil {
		t.Fatal("at with valid_from was accepted")
	}
	if _, _, err := eventInterval("last spring", "", ""); err == nil {
		t.Fatal("an unreadable time was accepted")
	}
}
