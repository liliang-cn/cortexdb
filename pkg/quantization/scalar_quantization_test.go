package quantization

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestScalarQuantizer(t *testing.T) {
	dim := 128
	nbits := 8
	
	sq, err := NewScalarQuantizer(dim, nbits)
	if err != nil {
		t.Fatalf("Failed to create scalar quantizer: %v", err)
	}
	
	if sq.Dimension != dim {
		t.Errorf("Expected dimension %d, got %d", dim, sq.Dimension)
	}
	
	if sq.NBits != nbits {
		t.Errorf("Expected %d bits, got %d", nbits, sq.NBits)
	}
}

func TestScalarQuantizerInvalidBits(t *testing.T) {
	// Test invalid bit values
	_, err := NewScalarQuantizer(128, 0)
	if err == nil {
		t.Error("Expected error for 0 bits")
	}
	
	_, err = NewScalarQuantizer(128, 9)
	if err == nil {
		t.Error("Expected error for >8 bits")
	}
}

func TestScalarQuantizerTrainEncodeDecode(t *testing.T) {
	dim := 64
	sq, _ := NewScalarQuantizer(dim, 4)
	
	// Generate training data
	vectors := generateTestVectorsPQ(100, dim)
	
	// Train
	err := sq.Train(vectors)
	if err != nil {
		t.Fatalf("Failed to train: %v", err)
	}
	
	if !sq.Trained {
		t.Error("Quantizer should be trained")
	}
	
	// Check min/max are set
	for d := 0; d < dim; d++ {
		if sq.Min[d] >= sq.Max[d] {
			t.Errorf("Invalid min/max for dimension %d", d)
		}
	}
	
	// Test encode/decode
	testVec := vectors[0]
	encoded, err := sq.Encode(testVec)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}
	
	// Check encoded size
	bitsNeeded := dim * sq.NBits
	bytesNeeded := (bitsNeeded + 7) / 8
	if len(encoded) != bytesNeeded {
		t.Errorf("Expected %d bytes, got %d", bytesNeeded, len(encoded))
	}
	
	// Decode
	decoded, err := sq.Decode(encoded)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	
	if len(decoded) != dim {
		t.Errorf("Expected decoded dimension %d, got %d", dim, len(decoded))
	}
	
	// Check reconstruction error
	mse := calculateMSE(testVec, decoded)
	t.Logf("Scalar quantization MSE (4 bits): %.6f", mse)
	
	// 4-bit quantization should have reasonable error
	if mse > 0.1 {
		t.Error("Reconstruction error too high for 4-bit quantization")
	}
}

func TestScalarQuantizerDifferentBits(t *testing.T) {
	dim := 32
	vectors := generateTestVectorsPQ(50, dim)
	
	testCases := []struct {
		bits        int
		maxMSE      float32
		compression float32
	}{
		{1, 1.5, 32.0},
		{2, 0.2, 16.0},
		{4, 0.05, 8.0},
		{8, 0.001, 4.0},
	}
	
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d_bits", tc.bits), func(t *testing.T) {
			sq, _ := NewScalarQuantizer(dim, tc.bits)
			if err := sq.Train(vectors); err != nil {
				t.Fatalf("Train failed: %v", err)
			}
			
			// Test compression ratio
			ratio := sq.CompressionRatio()
			if math.Abs(float64(ratio-tc.compression)) > 0.01 {
				t.Errorf("Expected compression ratio %.1f, got %.1f", tc.compression, ratio)
			}
			
			// Test reconstruction error
			totalMSE := float32(0)
			for _, vec := range vectors[:10] {
				encoded, _ := sq.Encode(vec)
				decoded, _ := sq.Decode(encoded)
				totalMSE += calculateMSE(vec, decoded)
			}
			avgMSE := totalMSE / 10
			
			t.Logf("%d-bit quantization MSE: %.6f", tc.bits, avgMSE)
			
			if avgMSE > tc.maxMSE {
				t.Errorf("MSE %.6f exceeds max %.6f for %d bits", avgMSE, tc.maxMSE, tc.bits)
			}
		})
	}
}

func TestBinaryQuantizer(t *testing.T) {
	dim := 128
	bq := NewBinaryQuantizer(dim)
	
	if bq.Dimension != dim {
		t.Errorf("Expected dimension %d, got %d", dim, bq.Dimension)
	}
}

func TestBinaryQuantizerTrainEncodeDecode(t *testing.T) {
	dim := 64
	bq := NewBinaryQuantizer(dim)
	
	// Generate training data
	vectors := generateTestVectorsPQ(100, dim)
	
	// Train
	err := bq.Train(vectors)
	if err != nil {
		t.Fatalf("Failed to train: %v", err)
	}
	
	if !bq.Trained {
		t.Error("Binary quantizer should be trained")
	}
	
	// Test encode/decode
	testVec := vectors[0]
	encoded, err := bq.Encode(testVec)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}
	
	// Check encoded size (1 bit per dimension)
	expectedBytes := (dim + 7) / 8
	if len(encoded) != expectedBytes {
		t.Errorf("Expected %d bytes, got %d", expectedBytes, len(encoded))
	}
	
	// Decode
	decoded, err := bq.Decode(encoded)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	
	if len(decoded) != dim {
		t.Errorf("Expected decoded dimension %d, got %d", dim, len(decoded))
	}
	
	// Check that values are around thresholds
	for i := 0; i < dim; i++ {
		diff := math.Abs(float64(decoded[i] - bq.Threshold[i]))
		if diff > 1.0 {
			t.Errorf("Decoded value too far from threshold at dimension %d", i)
		}
	}
}

func TestBinaryQuantizerHammingDistance(t *testing.T) {
	bq := NewBinaryQuantizer(32)
	
	// Test identical vectors
	a := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	b := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	dist := bq.HammingDistance(a, b)
	if dist != 0 {
		t.Errorf("Expected distance 0 for identical vectors, got %d", dist)
	}
	
	// Test completely different vectors
	c := []byte{0x00, 0x00, 0x00, 0x00}
	dist = bq.HammingDistance(a, c)
	if dist != 32 {
		t.Errorf("Expected distance 32 for opposite vectors, got %d", dist)
	}
	
	// Test one bit difference
	d := []byte{0xFE, 0xFF, 0xFF, 0xFF} // First bit different
	dist = bq.HammingDistance(a, d)
	if dist != 1 {
		t.Errorf("Expected distance 1 for one bit difference, got %d", dist)
	}
}

func TestBinaryQuantizerSearch(t *testing.T) {
	dim := 64
	bq := NewBinaryQuantizer(dim)
	
	// Generate and train
	vectors := generateTestVectorsPQ(100, dim)
	if err := bq.Train(vectors); err != nil {
		t.Fatalf("Train failed: %v", err)
	}
	
	// Encode all vectors
	database := make([][]byte, len(vectors))
	for i, vec := range vectors {
		encoded, _ := bq.Encode(vec)
		database[i] = encoded
	}
	
	// Search
	query, _ := bq.Encode(vectors[0])
	indices, distances := bq.SearchBinary(query, database, 5)
	
	if len(indices) != 5 {
		t.Errorf("Expected 5 results, got %d", len(indices))
	}
	
	// First result should be the query itself with distance 0
	if indices[0] != 0 {
		t.Errorf("Expected first result to be index 0, got %d", indices[0])
	}
	
	if distances[0] != 0 {
		t.Errorf("Expected distance 0 for exact match, got %d", distances[0])
	}
	
	// Distances should be in ascending order
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Error("Distances not in ascending order")
		}
	}
}

func TestBinaryQuantizerCompressionRatio(t *testing.T) {
	bq := NewBinaryQuantizer(512)
	
	ratio := bq.CompressionRatio()
	expectedRatio := float32(512*32) / float32(512) // 32 bits per float / 1 bit per dimension
	
	if ratio != expectedRatio {
		t.Errorf("Expected compression ratio %f, got %f", expectedRatio, ratio)
	}
}

func TestOptimizedBinaryQuantizer(t *testing.T) {
	inputDim := 128
	outputDim := 64
	
	obq := NewOptimizedBinaryQuantizer(inputDim, outputDim)
	
	if obq.Dimension != outputDim {
		t.Errorf("Expected output dimension %d, got %d", outputDim, obq.Dimension)
	}
	
	if len(obq.Projections) != outputDim {
		t.Errorf("Expected %d projections, got %d", outputDim, len(obq.Projections))
	}
	
	// Test projection
	vec := make([]float32, inputDim)
	for i := range vec {
		vec[i] = rand.Float32()
	}
	
	projected := obq.Project(vec)
	if len(projected) != outputDim {
		t.Errorf("Expected projected dimension %d, got %d", outputDim, len(projected))
	}
}

func BenchmarkScalarQuantizerEncode(b *testing.B) {
	sq, _ := NewScalarQuantizer(512, 8)
	vectors := generateTestVectorsPQ(1000, 512)
	if err := sq.Train(vectors); err != nil {
		b.Fatalf("Train failed: %v", err)
	}
	
	vec := vectors[0]
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		if _, err := sq.Encode(vec); err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
	}
}

func BenchmarkBinaryQuantizerEncode(b *testing.B) {
	bq := NewBinaryQuantizer(512)
	vectors := generateTestVectorsPQ(1000, 512)
	if err := bq.Train(vectors); err != nil {
		b.Fatalf("Train failed: %v", err)
	}
	
	vec := vectors[0]
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		if _, err := bq.Encode(vec); err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
	}
}

func BenchmarkHammingDistance(b *testing.B) {
	bq := NewBinaryQuantizer(512)
	
	// Create two random binary vectors
	a := make([]byte, 64)
	c := make([]byte, 64)
	for i := range a {
		a[i] = byte(rand.Intn(256))
		c[i] = byte(rand.Intn(256))
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		bq.HammingDistance(a, c)
	}
}