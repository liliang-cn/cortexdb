package quantization

import (
	"math"
	"math/rand"
	"testing"
)

func TestProductQuantizer(t *testing.T) {
	dim := 128
	numSubspaces := 8
	numCentroids := 16
	
	pq, err := NewProductQuantizer(dim, numSubspaces, numCentroids)
	if err != nil {
		t.Fatalf("Failed to create PQ: %v", err)
	}
	
	// Check initialization
	if pq.D != dim {
		t.Errorf("Expected dimension %d, got %d", dim, pq.D)
	}
	if pq.M != numSubspaces {
		t.Errorf("Expected %d subspaces, got %d", numSubspaces, pq.M)
	}
	if pq.K != numCentroids {
		t.Errorf("Expected %d centroids, got %d", numCentroids, pq.K)
	}
	if pq.SubDim != dim/numSubspaces {
		t.Errorf("Expected subdim %d, got %d", dim/numSubspaces, pq.SubDim)
	}
}

func TestProductQuantizerInvalidParams(t *testing.T) {
	// Test dimension not divisible by subspaces
	_, err := NewProductQuantizer(127, 8, 16)
	if err == nil {
		t.Error("Expected error for indivisible dimension")
	}
	
	// Test too many centroids
	_, err = NewProductQuantizer(128, 8, 257)
	if err == nil {
		t.Error("Expected error for >256 centroids")
	}
}

func TestProductQuantizerTrainEncodeDecode(t *testing.T) {
	dim := 64
	numVectors := 100
	
	pq, _ := NewProductQuantizer(dim, 4, 8)
	
	// Generate training vectors
	vectors := generateTestVectorsPQ(numVectors, dim)
	
	// Train
	err := pq.Train(vectors)
	if err != nil {
		t.Fatalf("Failed to train: %v", err)
	}
	
	if !pq.Trained {
		t.Error("PQ should be trained")
	}
	
	// Test encode/decode
	testVec := vectors[0]
	encoded, err := pq.Encode(testVec)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}
	
	// Check encoded size
	expectedBytes := pq.M // 1 byte per subspace
	if len(encoded) != expectedBytes {
		t.Errorf("Expected %d bytes, got %d", expectedBytes, len(encoded))
	}
	
	// Decode
	decoded, err := pq.Decode(encoded)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	
	if len(decoded) != dim {
		t.Errorf("Expected decoded dimension %d, got %d", dim, len(decoded))
	}
	
	// Check reconstruction error is reasonable
	mse := calculateMSE(testVec, decoded)
	t.Logf("Reconstruction MSE: %.6f", mse)
	
	if mse > 0.5 {
		t.Error("Reconstruction error too high")
	}
}

func TestProductQuantizerSearch(t *testing.T) {
	dim := 32
	numVectors := 50
	
	pq, _ := NewProductQuantizer(dim, 4, 8)
	
	// Generate and train
	vectors := generateTestVectorsPQ(numVectors, dim)
	if err := pq.Train(vectors); err != nil {
		t.Fatalf("Train failed: %v", err)
	}
	
	// Encode all vectors
	codes := make([][]byte, numVectors)
	for i, vec := range vectors {
		encoded, _ := pq.Encode(vec)
		codes[i] = encoded
	}
	
	// Search
	query := vectors[0]
	indices, distances := pq.SearchPQ(query, codes, 5)
	
	if len(indices) != 5 {
		t.Errorf("Expected 5 results, got %d", len(indices))
	}
	
	// First result should be the query itself
	if indices[0] != 0 {
		t.Errorf("Expected first result to be index 0, got %d", indices[0])
	}
	
	// Distances should be in ascending order
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Error("Distances not in ascending order")
		}
	}
}

func TestProductQuantizerCompressionRatio(t *testing.T) {
	pq, _ := NewProductQuantizer(512, 8, 256)
	
	ratio := pq.CompressionRatio()
	expectedRatio := float32(512*4) / float32(8) // 512 * 4 bytes / 8 bytes
	
	if math.Abs(float64(ratio-expectedRatio)) > 0.01 {
		t.Errorf("Expected compression ratio %.2f, got %.2f", expectedRatio, ratio)
	}
}

func TestProductQuantizerSerialization(t *testing.T) {
	dim := 16
	pq, _ := NewProductQuantizer(dim, 2, 4)
	
	// Train with some data
	vectors := generateTestVectorsPQ(20, dim)
	if err := pq.Train(vectors); err != nil {
		t.Fatalf("Train failed: %v", err)
	}
	
	// Serialize
	data := pq.SerializeCodebooks()
	if data == nil {
		t.Fatal("Serialization returned nil")
	}
	
	// Create new PQ and deserialize
	pq2, _ := NewProductQuantizer(dim, 2, 4)
	err := pq2.DeserializeCodebooks(data)
	if err != nil {
		t.Fatalf("Failed to deserialize: %v", err)
	}
	
	if !pq2.Trained {
		t.Error("Deserialized PQ should be trained")
	}
	
	// Test that encoding produces same results
	testVec := vectors[0]
	encoded1, _ := pq.Encode(testVec)
	encoded2, _ := pq2.Encode(testVec)
	
	for i := range encoded1 {
		if encoded1[i] != encoded2[i] {
			t.Error("Encoded results differ after serialization")
		}
	}
}

func TestProductQuantizerNotTrained(t *testing.T) {
	pq, _ := NewProductQuantizer(32, 4, 8)
	
	vec := make([]float32, 32)
	
	// Encode should fail
	_, err := pq.Encode(vec)
	if err == nil {
		t.Error("Expected error when encoding with untrained quantizer")
	}
	
	// Decode should fail
	_, err = pq.Decode([]byte{0, 0, 0, 0})
	if err == nil {
		t.Error("Expected error when decoding with untrained quantizer")
	}
}

func generateTestVectorsPQ(n, dim int) [][]float32 {
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vec := make([]float32, dim)
		for j := 0; j < dim; j++ {
			vec[j] = rng.Float32()*2 - 1
		}
		vectors[i] = vec
	}
	return vectors
}

func calculateMSE(a, b []float32) float32 {
	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return sum / float32(len(a))
}

func BenchmarkPQEncode(b *testing.B) {
	pq, _ := NewProductQuantizer(512, 8, 256)
	vectors := generateTestVectorsPQ(1000, 512)
	if err := pq.Train(vectors); err != nil {
		b.Fatalf("Train failed: %v", err)
	}
	
	vec := vectors[0]
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		if _, err := pq.Encode(vec); err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
	}
}

func BenchmarkPQDecode(b *testing.B) {
	pq, _ := NewProductQuantizer(512, 8, 256)
	vectors := generateTestVectorsPQ(1000, 512)
	if err := pq.Train(vectors); err != nil {
		b.Fatalf("Train failed: %v", err)
	}
	
	encoded, _ := pq.Encode(vectors[0])
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		if _, err := pq.Decode(encoded); err != nil {
			b.Fatalf("Decode failed: %v", err)
		}
	}
}

func BenchmarkPQSearch(b *testing.B) {
	pq, _ := NewProductQuantizer(128, 8, 256)
	vectors := generateTestVectorsPQ(10000, 128)
	if err := pq.Train(vectors); err != nil {
		b.Fatalf("Train failed: %v", err)
	}
	
	// Encode all vectors
	codes := make([][]byte, len(vectors))
	for i, vec := range vectors {
		codes[i], _ = pq.Encode(vec)
	}
	
	query := vectors[0]
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		pq.SearchPQ(query, codes, 10)
	}
}