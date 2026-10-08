// Package quantization - Scalar and Binary Quantization
package quantization

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"math/rand"
)

// ScalarQuantizer implements scalar quantization for vector compression
type ScalarQuantizer struct {
	Dimension int
	Min       []float32 // Min value per dimension
	Max       []float32 // Max value per dimension
	NBits     int       // Bits per component (1-8)
	Trained   bool

	// step is (Max-Min)/(2^NBits-1) per dimension: a code times its step
	// plus Min is the component. Derived from Min and Max, so not saved.
	step []float32
}

// steps returns the per-dimension step. Train and Load derive it; a
// quantizer filled in by hand derives it on each call rather than writing
// shared state from a reader.
func (sq *ScalarQuantizer) steps() []float32 {
	if len(sq.step) == sq.Dimension {
		return sq.step
	}
	return sq.computeSteps()
}

func (sq *ScalarQuantizer) computeSteps() []float32 {
	maxVal := float32((int(1) << uint(sq.NBits)) - 1)
	step := make([]float32, sq.Dimension)
	for d := range step {
		step[d] = (sq.Max[d] - sq.Min[d]) / maxVal
	}
	return step
}

// NewScalarQuantizer creates a new scalar quantizer
func NewScalarQuantizer(dimension int, nbits int) (*ScalarQuantizer, error) {
	if nbits < 1 || nbits > 8 {
		return nil, fmt.Errorf("nbits must be between 1 and 8, got %d", nbits)
	}

	return &ScalarQuantizer{
		Dimension: dimension,
		NBits:     nbits,
		Min:       make([]float32, dimension),
		Max:       make([]float32, dimension),
	}, nil
}

// Save serializes the quantizer
func (sq *ScalarQuantizer) Save(w io.Writer) error {
	enc := gob.NewEncoder(w)
	return enc.Encode(sq)
}

// Load deserializes the quantizer
func (sq *ScalarQuantizer) Load(r io.Reader) error {
	dec := gob.NewDecoder(r)
	if err := dec.Decode(sq); err != nil {
		return err
	}
	sq.step = sq.computeSteps()
	return nil
}

// Train learns the value ranges from training data
func (sq *ScalarQuantizer) Train(vectors [][]float32) error {
	if len(vectors) == 0 {
		return errors.New("no training vectors provided")
	}

	// Initialize min/max
	for d := 0; d < sq.Dimension; d++ {
		sq.Min[d] = vectors[0][d]
		sq.Max[d] = vectors[0][d]
	}

	// Find min/max for each dimension
	for _, vec := range vectors {
		if len(vec) != sq.Dimension {
			return fmt.Errorf("vector dimension %d doesn't match quantizer dimension %d", len(vec), sq.Dimension)
		}

		for d := 0; d < sq.Dimension; d++ {
			if vec[d] < sq.Min[d] {
				sq.Min[d] = vec[d]
			}
			if vec[d] > sq.Max[d] {
				sq.Max[d] = vec[d]
			}
		}
	}

	// Add small epsilon to avoid division by zero
	for d := 0; d < sq.Dimension; d++ {
		if sq.Max[d] == sq.Min[d] {
			sq.Max[d] += 1e-6
		}
	}

	sq.Trained = true
	sq.step = sq.computeSteps()
	return nil
}

// Encode quantizes a vector to bytes
func (sq *ScalarQuantizer) Encode(vector []float32) ([]byte, error) {
	if !sq.Trained {
		return nil, errors.New("quantizer not trained")
	}

	if len(vector) != sq.Dimension {
		return nil, fmt.Errorf("vector dimension %d doesn't match quantizer dimension %d", len(vector), sq.Dimension)
	}

	maxVal := float32((int(1) << uint(sq.NBits)) - 1)

	// Calculate bytes needed
	bitsNeeded := sq.Dimension * sq.NBits
	bytesNeeded := (bitsNeeded + 7) / 8
	encoded := make([]byte, bytesNeeded)

	bitOffset := 0
	for d := 0; d < sq.Dimension; d++ {
		// Normalize to [0, 1]
		normalized := (vector[d] - sq.Min[d]) / (sq.Max[d] - sq.Min[d])
		if normalized < 0 {
			normalized = 0
		} else if normalized > 1 {
			normalized = 1
		}

		// Quantize
		quantized := uint32(normalized * maxVal)

		// Pack bits
		for b := 0; b < sq.NBits; b++ {
			byteIdx := bitOffset / 8
			bitIdx := bitOffset % 8

			if (quantized & (1 << b)) != 0 {
				encoded[byteIdx] |= (1 << bitIdx)
			}

			bitOffset++
		}
	}

	return encoded, nil
}

// Decode reconstructs a vector from quantized bytes
func (sq *ScalarQuantizer) Decode(encoded []byte) ([]float32, error) {
	vector := make([]float32, sq.Dimension)
	if err := sq.DecodeInto(vector, encoded); err != nil {
		return nil, err
	}
	return vector, nil
}

// DecodeInto reconstructs a vector into dst, which must hold Dimension
// values. An index that keeps only codes decodes one for every distance it
// computes, so this is the hot path of a quantized search: it allocates
// nothing, and at 8 bits, where every byte is one component, it does not
// unpack bits at all.
func (sq *ScalarQuantizer) DecodeInto(dst []float32, encoded []byte) error {
	if !sq.Trained {
		return errors.New("quantizer not trained")
	}
	if len(dst) < sq.Dimension {
		return fmt.Errorf("destination holds %d values, need %d", len(dst), sq.Dimension)
	}
	maxVal := float32((int(1) << uint(sq.NBits)) - 1)

	if sq.NBits == 8 {
		if len(encoded) < sq.Dimension {
			return errors.New("encoded data too short")
		}
		step := sq.steps()
		for d := 0; d < sq.Dimension; d++ {
			dst[d] = float32(encoded[d])*step[d] + sq.Min[d]
		}
		return nil
	}

	bitOffset := 0
	for d := 0; d < sq.Dimension; d++ {
		// Unpack bits
		quantized := uint32(0)
		for b := 0; b < sq.NBits; b++ {
			byteIdx := bitOffset / 8
			bitIdx := bitOffset % 8

			if byteIdx >= len(encoded) {
				return errors.New("encoded data too short")
			}

			if (encoded[byteIdx] & (1 << bitIdx)) != 0 {
				quantized |= (1 << b)
			}

			bitOffset++
		}

		// Dequantize
		normalized := float32(quantized) / maxVal
		dst[d] = normalized*(sq.Max[d]-sq.Min[d]) + sq.Min[d]
	}
	return nil
}

// CosineDistanceTo is 1 - cosine(query, decoded code), computed on the code in
// one pass without materialising the decoded vector — the distance a graph
// of 8-bit codes computes for every edge it weighs. ok is false where the
// fast path does not apply (other bit widths, a short code, an untrained
// quantizer); the caller decodes and measures instead.
func (sq *ScalarQuantizer) CosineDistanceTo(query []float32, encoded []byte) (dist float32, ok bool) {
	if !sq.Trained || sq.NBits != 8 || len(encoded) < sq.Dimension || len(query) != sq.Dimension {
		return 0, false
	}
	step, lo := sq.steps(), sq.Min
	var dot, nq, nc float32
	for d, q := range query {
		c := float32(encoded[d])*step[d] + lo[d]
		dot += q * c
		nq += q * q
		nc += c * c
	}
	if nq == 0 || nc == 0 {
		return 1, true
	}
	return 1 - dot/(float32(math.Sqrt(float64(nq)))*float32(math.Sqrt(float64(nc)))), true
}

// CompressionRatio returns the compression ratio
func (sq *ScalarQuantizer) CompressionRatio() float32 {
	originalBits := sq.Dimension * 32 // 32 bits per float32
	compressedBits := sq.Dimension * sq.NBits
	return float32(originalBits) / float32(compressedBits)
}

// BinaryQuantizer implements binary quantization (1-bit vectors)
type BinaryQuantizer struct {
	Dimension int
	Threshold []float32 // Threshold per dimension
	Trained   bool
}

// NewBinaryQuantizer creates a new binary quantizer
func NewBinaryQuantizer(dimension int) *BinaryQuantizer {
	return &BinaryQuantizer{
		Dimension: dimension,
		Threshold: make([]float32, dimension),
	}
}

// Save serializes the quantizer
func (bq *BinaryQuantizer) Save(w io.Writer) error {
	enc := gob.NewEncoder(w)
	return enc.Encode(bq)
}

// Load deserializes the quantizer
func (bq *BinaryQuantizer) Load(r io.Reader) error {
	dec := gob.NewDecoder(r)
	return dec.Decode(bq)
}

// Train learns thresholds from training data
func (bq *BinaryQuantizer) Train(vectors [][]float32) error {
	if len(vectors) == 0 {
		return errors.New("no training vectors provided")
	}

	// Calculate mean for each dimension as threshold
	for d := 0; d < bq.Dimension; d++ {
		sum := float32(0)
		for _, vec := range vectors {
			if len(vec) != bq.Dimension {
				return fmt.Errorf("vector dimension %d doesn't match quantizer dimension %d", len(vec), bq.Dimension)
			}
			sum += vec[d]
		}
		bq.Threshold[d] = sum / float32(len(vectors))
	}

	bq.Trained = true
	return nil
}

// Encode binarizes a vector
func (bq *BinaryQuantizer) Encode(vector []float32) ([]byte, error) {
	if !bq.Trained {
		return nil, errors.New("quantizer not trained")
	}

	if len(vector) != bq.Dimension {
		return nil, fmt.Errorf("vector dimension %d doesn't match quantizer dimension %d", len(vector), bq.Dimension)
	}

	// Calculate bytes needed
	bytesNeeded := (bq.Dimension + 7) / 8
	encoded := make([]byte, bytesNeeded)

	for d := 0; d < bq.Dimension; d++ {
		if vector[d] > bq.Threshold[d] {
			byteIdx := d / 8
			bitIdx := d % 8
			encoded[byteIdx] |= (1 << bitIdx)
		}
	}

	return encoded, nil
}

// Decode reconstructs a vector from binary encoding
func (bq *BinaryQuantizer) Decode(encoded []byte) ([]float32, error) {
	if !bq.Trained {
		return nil, errors.New("quantizer not trained")
	}

	expectedBytes := (bq.Dimension + 7) / 8
	if len(encoded) != expectedBytes {
		return nil, fmt.Errorf("expected %d bytes, got %d", expectedBytes, len(encoded))
	}

	vector := make([]float32, bq.Dimension)

	for d := 0; d < bq.Dimension; d++ {
		byteIdx := d / 8
		bitIdx := d % 8

		if (encoded[byteIdx] & (1 << bitIdx)) != 0 {
			// Use a value above threshold
			vector[d] = bq.Threshold[d] + 0.5
		} else {
			// Use a value below threshold
			vector[d] = bq.Threshold[d] - 0.5
		}
	}

	return vector, nil
}

// HammingDistance computes Hamming distance between binary vectors
func (bq *BinaryQuantizer) HammingDistance(a, b []byte) int {
	if len(a) != len(b) {
		return -1
	}

	// One popcount per byte (math/bits compiles it to the hardware
	// instruction) rather than a loop per set bit.
	distance := 0
	for i := range a {
		distance += bits.OnesCount8(a[i] ^ b[i])
	}

	return distance
}

// SearchBinary performs fast binary search using Hamming distance
func (bq *BinaryQuantizer) SearchBinary(query []byte, database [][]byte, topK int) ([]int, []int) {
	type result struct {
		idx  int
		dist int
	}

	results := make([]result, len(database))
	for i, vec := range database {
		results[i] = result{
			idx:  i,
			dist: bq.HammingDistance(query, vec),
		}
	}

	// Sort by Hamming distance
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].dist < results[i].dist {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Return top-K
	k := topK
	if k > len(results) {
		k = len(results)
	}

	indices := make([]int, k)
	distances := make([]int, k)
	for i := 0; i < k; i++ {
		indices[i] = results[i].idx
		distances[i] = results[i].dist
	}

	return indices, distances
}

// CompressionRatio returns the compression ratio for binary quantization
func (bq *BinaryQuantizer) CompressionRatio() float32 {
	originalBits := bq.Dimension * 32 // 32 bits per float32
	compressedBits := bq.Dimension    // 1 bit per dimension
	return float32(originalBits) / float32(compressedBits)
}

// OptimizedBinaryQuantizer with learned projections
type OptimizedBinaryQuantizer struct {
	BinaryQuantizer
	Projections [][]float32 // Random projections for LSH
}

// NewOptimizedBinaryQuantizer creates an optimized binary quantizer with LSH
func NewOptimizedBinaryQuantizer(inputDim, outputDim int) *OptimizedBinaryQuantizer {
	obq := &OptimizedBinaryQuantizer{
		BinaryQuantizer: BinaryQuantizer{
			Dimension: outputDim,
			Threshold: make([]float32, outputDim),
		},
		Projections: make([][]float32, outputDim),
	}

	// Initialize random projections
	for i := 0; i < outputDim; i++ {
		obq.Projections[i] = make([]float32, inputDim)
		for j := 0; j < inputDim; j++ {
			// Gaussian random projection
			obq.Projections[i][j] = float32(randNormal()) / float32(math.Sqrt(float64(inputDim)))
		}
	}

	return obq
}

// Project applies random projections before binarization
func (obq *OptimizedBinaryQuantizer) Project(vector []float32) []float32 {
	projected := make([]float32, obq.Dimension)

	for i := 0; i < obq.Dimension; i++ {
		sum := float32(0)
		for j, val := range vector {
			sum += val * obq.Projections[i][j]
		}
		projected[i] = sum
	}

	return projected
}

// randNormal generates a random number from standard normal distribution
func randNormal() float64 {
	// Box-Muller transform
	u1 := rand.Float64()
	u2 := rand.Float64()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}
