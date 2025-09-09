// Package geo provides geo-spatial indexing and search capabilities for sqvect
package geo

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

const (
	// EarthRadiusKM is the Earth's radius in kilometers
	EarthRadiusKM = 6371.0
	// EarthRadiusMiles is the Earth's radius in miles
	EarthRadiusMiles = 3959.0
)

// DistanceUnit represents the unit for distance calculations
type DistanceUnit string

const (
	Kilometers DistanceUnit = "km"
	Miles      DistanceUnit = "miles"
	Meters     DistanceUnit = "meters"
)

// Coordinate represents a geographic coordinate
type Coordinate struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// GeoPoint represents a point with ID and coordinates
type GeoPoint struct {
	ID         string     `json:"id"`
	Coordinate Coordinate `json:"coordinate"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// GeoSearchResult represents a search result with distance
type GeoSearchResult struct {
	Point    GeoPoint `json:"point"`
	Distance float64  `json:"distance"`
}

// BoundingBox represents a rectangular geographic area
type BoundingBox struct {
	MinLat float64 `json:"min_lat"`
	MaxLat float64 `json:"max_lat"`
	MinLng float64 `json:"min_lng"`
	MaxLng float64 `json:"max_lng"`
}

// GeoIndex provides geo-spatial indexing and search
type GeoIndex struct {
	mu     sync.RWMutex
	points map[string]*GeoPoint
	// Grid-based index for faster spatial queries
	grid   map[int64][]*GeoPoint
	gridSize float64 // Size of each grid cell in degrees
}

// NewGeoIndex creates a new geo-spatial index
func NewGeoIndex() *GeoIndex {
	return &GeoIndex{
		points:   make(map[string]*GeoPoint),
		grid:     make(map[int64][]*GeoPoint),
		gridSize: 0.1, // 0.1 degree grid cells (~11km at equator)
	}
}

// Insert adds a geo point to the index
func (g *GeoIndex) Insert(point GeoPoint) error {
	if point.ID == "" {
		return fmt.Errorf("point ID cannot be empty")
	}
	
	if !isValidCoordinate(point.Coordinate) {
		return fmt.Errorf("invalid coordinate: lat=%f, lng=%f", point.Coordinate.Lat, point.Coordinate.Lng)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Store point
	g.points[point.ID] = &point

	// Add to grid index
	gridKey := g.getGridKey(point.Coordinate)
	g.grid[gridKey] = append(g.grid[gridKey], &point)

	return nil
}

// Delete removes a point from the index
func (g *GeoIndex) Delete(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	point, exists := g.points[id]
	if !exists {
		return false
	}

	// Remove from main index
	delete(g.points, id)

	// Remove from grid index
	gridKey := g.getGridKey(point.Coordinate)
	if cells, ok := g.grid[gridKey]; ok {
		for i, p := range cells {
			if p.ID == id {
				// Remove element by swapping with last and truncating
				g.grid[gridKey][i] = cells[len(cells)-1]
				g.grid[gridKey] = cells[:len(cells)-1]
				break
			}
		}
		// Clean up empty cells
		if len(g.grid[gridKey]) == 0 {
			delete(g.grid, gridKey)
		}
	}

	return true
}

// SearchRadius finds all points within a given radius from a center point
func (g *GeoIndex) SearchRadius(center Coordinate, radius float64, unit DistanceUnit) ([]GeoSearchResult, error) {
	if !isValidCoordinate(center) {
		return nil, fmt.Errorf("invalid center coordinate")
	}
	
	if radius <= 0 {
		return nil, fmt.Errorf("radius must be positive")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Convert radius to kilometers for calculations
	radiusKM := convertToKM(radius, unit)
	
	// Get candidate cells from grid
	candidates := g.getCandidateCells(center, radiusKM)
	
	// Calculate distances and filter
	var results []GeoSearchResult
	for _, point := range candidates {
		dist := haversineDistance(center, point.Coordinate)
		if dist <= radiusKM {
			// Convert distance back to requested unit
			displayDist := convertFromKM(dist, unit)
			results = append(results, GeoSearchResult{
				Point:    *point,
				Distance: displayDist,
			})
		}
	}

	// Sort by distance
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	return results, nil
}

// SearchKNN finds the k nearest neighbors to a point
func (g *GeoIndex) SearchKNN(center Coordinate, k int) ([]GeoSearchResult, error) {
	if !isValidCoordinate(center) {
		return nil, fmt.Errorf("invalid center coordinate")
	}
	
	if k <= 0 {
		return nil, fmt.Errorf("k must be positive")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Calculate distances to all points
	results := make([]GeoSearchResult, 0, len(g.points))
	for _, point := range g.points {
		dist := haversineDistance(center, point.Coordinate)
		results = append(results, GeoSearchResult{
			Point:    *point,
			Distance: dist,
		})
	}

	// Sort by distance
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	// Return top k
	if k > len(results) {
		return results, nil
	}
	return results[:k], nil
}

// SearchBoundingBox finds all points within a bounding box
func (g *GeoIndex) SearchBoundingBox(bbox BoundingBox) ([]GeoPoint, error) {
	if !isValidBoundingBox(bbox) {
		return nil, fmt.Errorf("invalid bounding box")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	var results []GeoPoint
	
	// Get all grid cells that intersect with bounding box
	minGridX := int64(bbox.MinLng / g.gridSize)
	maxGridX := int64(bbox.MaxLng / g.gridSize)
	minGridY := int64(bbox.MinLat / g.gridSize)
	maxGridY := int64(bbox.MaxLat / g.gridSize)

	for x := minGridX; x <= maxGridX; x++ {
		for y := minGridY; y <= maxGridY; y++ {
			gridKey := (x << 32) | (y & 0xFFFFFFFF)
			if cells, ok := g.grid[gridKey]; ok {
				for _, point := range cells {
					if point.Coordinate.Lat >= bbox.MinLat && 
					   point.Coordinate.Lat <= bbox.MaxLat &&
					   point.Coordinate.Lng >= bbox.MinLng && 
					   point.Coordinate.Lng <= bbox.MaxLng {
						results = append(results, *point)
					}
				}
			}
		}
	}

	return results, nil
}

// SearchPolygon finds all points within a polygon (simplified for convex polygons)
func (g *GeoIndex) SearchPolygon(polygon []Coordinate) ([]GeoPoint, error) {
	if len(polygon) < 3 {
		return nil, fmt.Errorf("polygon must have at least 3 points")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Get bounding box of polygon for initial filtering
	bbox := getBoundingBoxFromPolygon(polygon)
	
	var results []GeoPoint
	for _, point := range g.points {
		// Quick bounding box check
		if point.Coordinate.Lat < bbox.MinLat || point.Coordinate.Lat > bbox.MaxLat ||
		   point.Coordinate.Lng < bbox.MinLng || point.Coordinate.Lng > bbox.MaxLng {
			continue
		}
		
		// Point-in-polygon test using ray casting algorithm
		if pointInPolygon(point.Coordinate, polygon) {
			results = append(results, *point)
		}
	}

	return results, nil
}

// Size returns the number of points in the index
func (g *GeoIndex) Size() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.points)
}

// Clear removes all points from the index
func (g *GeoIndex) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.points = make(map[string]*GeoPoint)
	g.grid = make(map[int64][]*GeoPoint)
}

// GetPoint retrieves a point by ID
func (g *GeoIndex) GetPoint(id string) (*GeoPoint, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	point, exists := g.points[id]
	if exists {
		// Return a copy to prevent external modification
		copy := *point
		return &copy, true
	}
	return nil, false
}

// Helper functions

// getGridKey calculates the grid cell key for a coordinate
func (g *GeoIndex) getGridKey(coord Coordinate) int64 {
	gridX := int64(coord.Lng / g.gridSize)
	gridY := int64(coord.Lat / g.gridSize)
	// Combine x and y into a single int64 key
	return (gridX << 32) | (gridY & 0xFFFFFFFF)
}

// getCandidateCells returns points from grid cells that might contain results
func (g *GeoIndex) getCandidateCells(center Coordinate, radiusKM float64) []*GeoPoint {
	// Calculate how many grid cells we need to check
	// At equator, 1 degree ≈ 111km
	degreesRadius := radiusKM / 111.0
	cellsRadius := int64(math.Ceil(degreesRadius / g.gridSize))
	
	centerGridX := int64(center.Lng / g.gridSize)
	centerGridY := int64(center.Lat / g.gridSize)
	
	var candidates []*GeoPoint
	for dx := -cellsRadius; dx <= cellsRadius; dx++ {
		for dy := -cellsRadius; dy <= cellsRadius; dy++ {
			gridKey := ((centerGridX + dx) << 32) | ((centerGridY + dy) & 0xFFFFFFFF)
			if cells, ok := g.grid[gridKey]; ok {
				candidates = append(candidates, cells...)
			}
		}
	}
	
	return candidates
}

// haversineDistance calculates the great-circle distance between two points in kilometers
func haversineDistance(p1, p2 Coordinate) float64 {
	lat1Rad := p1.Lat * math.Pi / 180
	lat2Rad := p2.Lat * math.Pi / 180
	deltaLat := (p2.Lat - p1.Lat) * math.Pi / 180
	deltaLng := (p2.Lng - p1.Lng) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLng/2)*math.Sin(deltaLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return EarthRadiusKM * c
}

// isValidCoordinate checks if a coordinate is valid
func isValidCoordinate(coord Coordinate) bool {
	return coord.Lat >= -90 && coord.Lat <= 90 &&
		coord.Lng >= -180 && coord.Lng <= 180
}

// isValidBoundingBox checks if a bounding box is valid
func isValidBoundingBox(bbox BoundingBox) bool {
	return bbox.MinLat >= -90 && bbox.MaxLat <= 90 &&
		bbox.MinLng >= -180 && bbox.MaxLng <= 180 &&
		bbox.MinLat <= bbox.MaxLat &&
		bbox.MinLng <= bbox.MaxLng
}

// convertToKM converts distance to kilometers
func convertToKM(distance float64, unit DistanceUnit) float64 {
	switch unit {
	case Miles:
		return distance * 1.60934
	case Meters:
		return distance / 1000.0
	default: // Kilometers
		return distance
	}
}

// convertFromKM converts distance from kilometers
func convertFromKM(distance float64, unit DistanceUnit) float64 {
	switch unit {
	case Miles:
		return distance / 1.60934
	case Meters:
		return distance * 1000.0
	default: // Kilometers
		return distance
	}
}

// getBoundingBoxFromPolygon calculates the bounding box of a polygon
func getBoundingBoxFromPolygon(polygon []Coordinate) BoundingBox {
	bbox := BoundingBox{
		MinLat: polygon[0].Lat,
		MaxLat: polygon[0].Lat,
		MinLng: polygon[0].Lng,
		MaxLng: polygon[0].Lng,
	}
	
	for _, coord := range polygon[1:] {
		if coord.Lat < bbox.MinLat {
			bbox.MinLat = coord.Lat
		}
		if coord.Lat > bbox.MaxLat {
			bbox.MaxLat = coord.Lat
		}
		if coord.Lng < bbox.MinLng {
			bbox.MinLng = coord.Lng
		}
		if coord.Lng > bbox.MaxLng {
			bbox.MaxLng = coord.Lng
		}
	}
	
	return bbox
}

// pointInPolygon tests if a point is inside a polygon using ray casting
func pointInPolygon(point Coordinate, polygon []Coordinate) bool {
	inside := false
	p1 := polygon[0]
	
	for i := 1; i <= len(polygon); i++ {
		p2 := polygon[i%len(polygon)]
		
		if point.Lng > math.Min(p1.Lng, p2.Lng) {
			if point.Lng <= math.Max(p1.Lng, p2.Lng) {
				if point.Lat <= math.Max(p1.Lat, p2.Lat) {
					if p1.Lng != p2.Lng {
						xinters := (point.Lng-p1.Lng)*(p2.Lat-p1.Lat)/(p2.Lng-p1.Lng) + p1.Lat
						if p1.Lat == p2.Lat || point.Lat <= xinters {
							inside = !inside
						}
					}
				}
			}
		}
		p1 = p2
	}
	
	return inside
}