package geo

import (
	"math"
	"testing"
)

func TestAntipodalPointRemainsSearchable(t *testing.T) {
	center := Coordinate{Lat: 12.10701146710268, Lng: 46.5963454870261}
	opposite := Coordinate{Lat: -center.Lat, Lng: center.Lng - 180}
	index := NewGeoIndex()
	if err := index.Insert(GeoPoint{ID: "antipode", Coordinate: opposite}); err != nil {
		t.Fatal(err)
	}
	// Opposite points on this sphere are separated by exactly half its circumference.
	want := math.Pi * EarthRadiusKM
	t.Run("nearest distance", func(t *testing.T) {
		nearest, err := index.SearchKNN(center, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(nearest) != 1 || math.IsNaN(nearest[0].Distance) || math.Abs(nearest[0].Distance-want) > 0.001 {
			t.Fatalf("nearest antipode = %+v, want finite distance %.6f km", nearest, want)
		}
	})
	t.Run("radius includes antipode", func(t *testing.T) {
		within, err := index.SearchRadius(center, want+0.001, Kilometers)
		if err != nil {
			t.Fatal(err)
		}
		if len(within) != 1 || within[0].Point.ID != "antipode" {
			t.Fatalf("whole-sphere radius omitted antipode: %+v", within)
		}
	})
	t.Run("smaller radius excludes antipode", func(t *testing.T) {
		within, err := index.SearchRadius(center, want-1, Kilometers)
		if err != nil {
			t.Fatal(err)
		}
		if len(within) != 0 {
			t.Fatalf("smaller radius includes antipode: %+v", within)
		}
	})
}
