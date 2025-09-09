package geo

import (
	"fmt"
	"math"
	"testing"
)

func TestGeoIndexBasic(t *testing.T) {
	index := NewGeoIndex()

	// Test data - Major cities
	cities := []GeoPoint{
		{ID: "nyc", Coordinate: Coordinate{Lat: 40.7128, Lng: -74.0060}},      // New York
		{ID: "london", Coordinate: Coordinate{Lat: 51.5074, Lng: -0.1278}},    // London
		{ID: "tokyo", Coordinate: Coordinate{Lat: 35.6762, Lng: 139.6503}},    // Tokyo
		{ID: "sydney", Coordinate: Coordinate{Lat: -33.8688, Lng: 151.2093}},  // Sydney
		{ID: "paris", Coordinate: Coordinate{Lat: 48.8566, Lng: 2.3522}},      // Paris
		{ID: "sf", Coordinate: Coordinate{Lat: 37.7749, Lng: -122.4194}},      // San Francisco
		{ID: "la", Coordinate: Coordinate{Lat: 34.0522, Lng: -118.2437}},      // Los Angeles
	}

	// Insert cities
	for _, city := range cities {
		if err := index.Insert(city); err != nil {
			t.Fatalf("Failed to insert %s: %v", city.ID, err)
		}
	}

	if index.Size() != len(cities) {
		t.Errorf("Expected size %d, got %d", len(cities), index.Size())
	}

	// Test GetPoint
	if point, exists := index.GetPoint("nyc"); !exists {
		t.Error("NYC should exist in index")
	} else if point.Coordinate.Lat != 40.7128 {
		t.Errorf("NYC latitude mismatch: expected 40.7128, got %f", point.Coordinate.Lat)
	}

	// Test Delete
	if !index.Delete("tokyo") {
		t.Error("Failed to delete Tokyo")
	}

	if index.Size() != len(cities)-1 {
		t.Errorf("Expected size %d after deletion, got %d", len(cities)-1, index.Size())
	}

	if _, exists := index.GetPoint("tokyo"); exists {
		t.Error("Tokyo should not exist after deletion")
	}
}

func TestRadiusSearch(t *testing.T) {
	index := NewGeoIndex()

	// California cities
	cities := []GeoPoint{
		{ID: "sf", Coordinate: Coordinate{Lat: 37.7749, Lng: -122.4194}},       // San Francisco
		{ID: "oakland", Coordinate: Coordinate{Lat: 37.8044, Lng: -122.2712}},  // Oakland (~13km from SF)
		{ID: "berkeley", Coordinate: Coordinate{Lat: 37.8716, Lng: -122.2727}}, // Berkeley (~16km from SF)
		{ID: "sanjose", Coordinate: Coordinate{Lat: 37.3382, Lng: -121.8863}},  // San Jose (~76km from SF)
		{ID: "sacramento", Coordinate: Coordinate{Lat: 38.5816, Lng: -121.4944}}, // Sacramento (~120km from SF)
		{ID: "la", Coordinate: Coordinate{Lat: 34.0522, Lng: -118.2437}},       // Los Angeles (~560km from SF)
	}

	for _, city := range cities {
		_ = index.Insert(city)
	}

	// Search within 20km of San Francisco
	sfCoord := Coordinate{Lat: 37.7749, Lng: -122.4194}
	results, err := index.SearchRadius(sfCoord, 20, Kilometers)
	if err != nil {
		t.Fatalf("Radius search failed: %v", err)
	}

	// Should find SF, Oakland, and Berkeley
	if len(results) != 3 {
		t.Errorf("Expected 3 cities within 20km of SF, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s: %.2f km", r.Point.ID, r.Distance)
		}
	}

	// Verify SF is first (closest)
	if len(results) > 0 && results[0].Point.ID != "sf" {
		t.Errorf("Expected SF to be closest, got %s", results[0].Point.ID)
	}

	// Test with miles
	resultsMiles, err := index.SearchRadius(sfCoord, 12, Miles)
	if err != nil {
		t.Fatalf("Radius search in miles failed: %v", err)
	}

	// 12 miles ≈ 19.3 km, should find SF, Oakland, Berkeley
	if len(resultsMiles) != 3 {
		t.Errorf("Expected 3 cities within 12 miles of SF, got %d", len(resultsMiles))
	}

	// Test with meters
	resultsMeters, err := index.SearchRadius(sfCoord, 15000, Meters)
	if err != nil {
		t.Fatalf("Radius search in meters failed: %v", err)
	}

	// 15000 meters = 15 km, should find SF and Oakland
	if len(resultsMeters) != 2 {
		t.Errorf("Expected 2 cities within 15000 meters of SF, got %d", len(resultsMeters))
	}
}

func TestKNNSearch(t *testing.T) {
	index := NewGeoIndex()

	// European cities
	cities := []GeoPoint{
		{ID: "london", Coordinate: Coordinate{Lat: 51.5074, Lng: -0.1278}},
		{ID: "paris", Coordinate: Coordinate{Lat: 48.8566, Lng: 2.3522}},
		{ID: "berlin", Coordinate: Coordinate{Lat: 52.5200, Lng: 13.4050}},
		{ID: "rome", Coordinate: Coordinate{Lat: 41.9028, Lng: 12.4964}},
		{ID: "madrid", Coordinate: Coordinate{Lat: 40.4168, Lng: -3.7038}},
		{ID: "amsterdam", Coordinate: Coordinate{Lat: 52.3676, Lng: 4.9041}},
	}

	for _, city := range cities {
		_ = index.Insert(city)
	}

	// Find 3 nearest neighbors to Brussels
	brussels := Coordinate{Lat: 50.8503, Lng: 4.3517}
	results, err := index.SearchKNN(brussels, 3)
	if err != nil {
		t.Fatalf("KNN search failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 nearest neighbors, got %d", len(results))
	}

	// Amsterdam should be closest to Brussels (~170km)
	if len(results) > 0 && results[0].Point.ID != "amsterdam" {
		t.Errorf("Expected Amsterdam to be closest to Brussels, got %s", results[0].Point.ID)
	}

	// Test with k larger than dataset
	allResults, err := index.SearchKNN(brussels, 10)
	if err != nil {
		t.Fatalf("KNN search with large k failed: %v", err)
	}

	if len(allResults) != 6 {
		t.Errorf("Expected all 6 cities when k=10, got %d", len(allResults))
	}
}

func TestBoundingBoxSearch(t *testing.T) {
	index := NewGeoIndex()

	// US cities
	cities := []GeoPoint{
		{ID: "nyc", Coordinate: Coordinate{Lat: 40.7128, Lng: -74.0060}},
		{ID: "boston", Coordinate: Coordinate{Lat: 42.3601, Lng: -71.0589}},
		{ID: "philly", Coordinate: Coordinate{Lat: 39.9526, Lng: -75.1652}},
		{ID: "dc", Coordinate: Coordinate{Lat: 38.9072, Lng: -77.0369}},
		{ID: "miami", Coordinate: Coordinate{Lat: 25.7617, Lng: -80.1918}},
		{ID: "atlanta", Coordinate: Coordinate{Lat: 33.7490, Lng: -84.3880}},
	}

	for _, city := range cities {
		_ = index.Insert(city)
	}

	// Bounding box for Northeast US (roughly NYC to Boston area)
	bbox := BoundingBox{
		MinLat: 39.0,
		MaxLat: 43.0,
		MinLng: -76.0,
		MaxLng: -70.0,
	}

	results, err := index.SearchBoundingBox(bbox)
	if err != nil {
		t.Fatalf("Bounding box search failed: %v", err)
	}

	// Should find NYC, Boston, Philly
	expectedCities := map[string]bool{
		"nyc":    true,
		"boston": true,
		"philly": true,
	}

	if len(results) != len(expectedCities) {
		t.Errorf("Expected %d cities in bounding box, got %d", len(expectedCities), len(results))
	}

	for _, city := range results {
		if !expectedCities[city.ID] {
			t.Errorf("Unexpected city in bounding box: %s", city.ID)
		}
	}
}

func TestPolygonSearch(t *testing.T) {
	index := NewGeoIndex()

	// Points for testing
	points := []GeoPoint{
		{ID: "inside1", Coordinate: Coordinate{Lat: 40.0, Lng: -74.0}},
		{ID: "inside2", Coordinate: Coordinate{Lat: 40.5, Lng: -73.5}},
		{ID: "outside1", Coordinate: Coordinate{Lat: 42.0, Lng: -74.0}},
		{ID: "outside2", Coordinate: Coordinate{Lat: 39.0, Lng: -72.0}},
		{ID: "boundary", Coordinate: Coordinate{Lat: 40.0, Lng: -73.0}},
	}

	for _, point := range points {
		_ = index.Insert(point)
	}

	// Triangle polygon
	polygon := []Coordinate{
		{Lat: 39.5, Lng: -74.5},
		{Lat: 41.0, Lng: -74.5},
		{Lat: 40.25, Lng: -72.5},
	}

	results, err := index.SearchPolygon(polygon)
	if err != nil {
		t.Fatalf("Polygon search failed: %v", err)
	}

	// Check results
	foundIDs := make(map[string]bool)
	for _, point := range results {
		foundIDs[point.ID] = true
	}

	// inside1 and inside2 should be found
	if !foundIDs["inside1"] {
		t.Error("Expected inside1 to be in polygon")
	}
	if !foundIDs["inside2"] {
		t.Error("Expected inside2 to be in polygon")
	}
	if foundIDs["outside1"] {
		t.Error("outside1 should not be in polygon")
	}
	if foundIDs["outside2"] {
		t.Error("outside2 should not be in polygon")
	}
}

func TestInvalidInputs(t *testing.T) {
	index := NewGeoIndex()

	// Test invalid coordinates
	invalidPoints := []GeoPoint{
		{ID: "invalid1", Coordinate: Coordinate{Lat: 91, Lng: 0}},    // Lat > 90
		{ID: "invalid2", Coordinate: Coordinate{Lat: -91, Lng: 0}},   // Lat < -90
		{ID: "invalid3", Coordinate: Coordinate{Lat: 0, Lng: 181}},   // Lng > 180
		{ID: "invalid4", Coordinate: Coordinate{Lat: 0, Lng: -181}},  // Lng < -180
	}

	for _, point := range invalidPoints {
		if err := index.Insert(point); err == nil {
			t.Errorf("Expected error for invalid point %s", point.ID)
		}
	}

	// Test empty ID
	if err := index.Insert(GeoPoint{Coordinate: Coordinate{Lat: 0, Lng: 0}}); err == nil {
		t.Error("Expected error for empty ID")
	}

	// Test invalid radius
	validCoord := Coordinate{Lat: 40, Lng: -74}
	if _, err := index.SearchRadius(validCoord, -10, Kilometers); err == nil {
		t.Error("Expected error for negative radius")
	}
	if _, err := index.SearchRadius(validCoord, 0, Kilometers); err == nil {
		t.Error("Expected error for zero radius")
	}

	// Test invalid k
	if _, err := index.SearchKNN(validCoord, 0); err == nil {
		t.Error("Expected error for k=0")
	}
	if _, err := index.SearchKNN(validCoord, -1); err == nil {
		t.Error("Expected error for negative k")
	}

	// Test invalid bounding box
	invalidBBox := BoundingBox{
		MinLat: 40,
		MaxLat: 30, // Max < Min
		MinLng: -80,
		MaxLng: -70,
	}
	if _, err := index.SearchBoundingBox(invalidBBox); err == nil {
		t.Error("Expected error for invalid bounding box")
	}

	// Test invalid polygon
	if _, err := index.SearchPolygon([]Coordinate{{Lat: 0, Lng: 0}}); err == nil {
		t.Error("Expected error for polygon with < 3 points")
	}
}

func TestHaversineDistance(t *testing.T) {
	// Test known distances
	testCases := []struct {
		name     string
		p1       Coordinate
		p2       Coordinate
		expected float64 // in km
		tolerance float64
	}{
		{
			name:     "Same point",
			p1:       Coordinate{Lat: 40.7128, Lng: -74.0060},
			p2:       Coordinate{Lat: 40.7128, Lng: -74.0060},
			expected: 0,
			tolerance: 0.01,
		},
		{
			name:     "NYC to London",
			p1:       Coordinate{Lat: 40.7128, Lng: -74.0060},
			p2:       Coordinate{Lat: 51.5074, Lng: -0.1278},
			expected: 5570, // Approximately 5570 km
			tolerance: 10,
		},
		{
			name:     "Equator points",
			p1:       Coordinate{Lat: 0, Lng: 0},
			p2:       Coordinate{Lat: 0, Lng: 1},
			expected: 111.32, // 1 degree at equator
			tolerance: 0.5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dist := haversineDistance(tc.p1, tc.p2)
			diff := math.Abs(dist - tc.expected)
			if diff > tc.tolerance {
				t.Errorf("Distance %s: expected %.2f±%.2f km, got %.2f km", 
					tc.name, tc.expected, tc.tolerance, dist)
			}
		})
	}
}

func TestClear(t *testing.T) {
	index := NewGeoIndex()

	// Add some points
	for i := 0; i < 10; i++ {
		point := GeoPoint{
			ID:         fmt.Sprintf("point%d", i),
			Coordinate: Coordinate{Lat: float64(i), Lng: float64(i)},
		}
		_ = index.Insert(point)
	}

	if index.Size() != 10 {
		t.Errorf("Expected 10 points, got %d", index.Size())
	}

	// Clear index
	index.Clear()

	if index.Size() != 0 {
		t.Errorf("Expected 0 points after clear, got %d", index.Size())
	}

	// Should be able to insert after clear
	_ = index.Insert(GeoPoint{ID: "new", Coordinate: Coordinate{Lat: 0, Lng: 0}})
	if index.Size() != 1 {
		t.Errorf("Expected 1 point after insert, got %d", index.Size())
	}
}

func TestUnitConversion(t *testing.T) {
	testCases := []struct {
		value    float64
		from     DistanceUnit
		expected float64
	}{
		{10, Miles, 16.0934},       // 10 miles to km
		{10, Kilometers, 10},        // 10 km to km
		{10000, Meters, 10},         // 10000 meters to km
	}

	for _, tc := range testCases {
		result := convertToKM(tc.value, tc.from)
		if math.Abs(result-tc.expected) > 0.01 {
			t.Errorf("Convert %f %s to km: expected %.2f, got %.2f", 
				tc.value, tc.from, tc.expected, result)
		}
	}

	// Test reverse conversion
	for _, tc := range testCases {
		result := convertFromKM(tc.expected, tc.from)
		if math.Abs(result-tc.value) > 0.01 {
			t.Errorf("Convert %.2f km to %s: expected %f, got %.2f", 
				tc.expected, tc.from, tc.value, result)
		}
	}
}

func BenchmarkInsert(b *testing.B) {
	index := NewGeoIndex()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		point := GeoPoint{
			ID: fmt.Sprintf("point%d", i),
			Coordinate: Coordinate{
				Lat: float64(i%180) - 90,
				Lng: float64(i%360) - 180,
			},
		}
		_ = index.Insert(point)
	}
}

func BenchmarkRadiusSearch(b *testing.B) {
	index := NewGeoIndex()
	
	// Insert 10000 random points
	for i := 0; i < 10000; i++ {
		point := GeoPoint{
			ID: fmt.Sprintf("point%d", i),
			Coordinate: Coordinate{
				Lat: float64(i%180) - 90,
				Lng: float64(i%360) - 180,
			},
		}
		_ = index.Insert(point)
	}

	center := Coordinate{Lat: 0, Lng: 0}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = index.SearchRadius(center, 100, Kilometers)
	}
}

func BenchmarkKNNSearch(b *testing.B) {
	index := NewGeoIndex()
	
	// Insert 10000 random points
	for i := 0; i < 10000; i++ {
		point := GeoPoint{
			ID: fmt.Sprintf("point%d", i),
			Coordinate: Coordinate{
				Lat: float64(i%180) - 90,
				Lng: float64(i%360) - 180,
			},
		}
		_ = index.Insert(point)
	}

	center := Coordinate{Lat: 0, Lng: 0}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = index.SearchKNN(center, 10)
	}
}

func BenchmarkBoundingBoxSearch(b *testing.B) {
	index := NewGeoIndex()
	
	// Insert 10000 random points
	for i := 0; i < 10000; i++ {
		point := GeoPoint{
			ID: fmt.Sprintf("point%d", i),
			Coordinate: Coordinate{
				Lat: float64(i%180) - 90,
				Lng: float64(i%360) - 180,
			},
		}
		_ = index.Insert(point)
	}

	bbox := BoundingBox{
		MinLat: -10,
		MaxLat: 10,
		MinLng: -10,
		MaxLng: 10,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = index.SearchBoundingBox(bbox)
	}
}