package sqvect

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// encodeVector converts a float32 slice to bytes using little-endian encoding
func encodeVector(vector []float32) ([]byte, error) {
	if vector == nil {
		return nil, ErrInvalidVector
	}
	
	buf := new(bytes.Buffer)
	
	// Write the length first - check for overflow
	vectorLen := len(vector)
	if vectorLen > 2147483647 { // max int32
		return nil, fmt.Errorf("vector too large: %d elements exceeds maximum", vectorLen)
	}
	if err := binary.Write(buf, binary.LittleEndian, int32(vectorLen)); err != nil {
		return nil, fmt.Errorf("failed to encode vector length: %w", err)
	}
	
	// Write each float32 value
	for _, val := range vector {
		if err := binary.Write(buf, binary.LittleEndian, val); err != nil {
			return nil, fmt.Errorf("failed to encode vector value: %w", err)
		}
	}
	
	return buf.Bytes(), nil
}

// decodeVector converts bytes back to a float32 slice using little-endian encoding
func decodeVector(data []byte) ([]float32, error) {
	if len(data) < 4 {
		return nil, ErrInvalidVector
	}
	
	buf := bytes.NewReader(data)
	
	// Read the length first
	var length int32
	if err := binary.Read(buf, binary.LittleEndian, &length); err != nil {
		return nil, fmt.Errorf("failed to decode vector length: %w", err)
	}
	
	if length < 0 {
		return nil, ErrInvalidVector
	}
	
	if length == 0 {
		return []float32{}, nil
	}
	
	// Check if we have enough bytes for the vector
	expectedBytes := int(length) * 4 // 4 bytes per float32
	if buf.Len() < expectedBytes {
		return nil, ErrInvalidVector
	}
	
	// Read the vector values
	vector := make([]float32, length)
	for i := int32(0); i < length; i++ {
		if err := binary.Read(buf, binary.LittleEndian, &vector[i]); err != nil {
			return nil, fmt.Errorf("failed to decode vector value at index %d: %w", i, err)
		}
	}
	
	return vector, nil
}

// encodeMetadata converts metadata map to JSON string
func encodeMetadata(metadata map[string]string) (string, error) {
	if metadata == nil {
		return "", nil
	}
	
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("failed to encode metadata: %w", err)
	}
	
	return string(data), nil
}

// decodeMetadata converts JSON string back to metadata map
func decodeMetadata(jsonStr string) (map[string]string, error) {
	if jsonStr == "" {
		return nil, nil
	}
	
	var metadata map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &metadata); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}
	
	return metadata, nil
}

// validateEmbedding performs comprehensive validation of an embedding
func validateEmbedding(emb Embedding, expectedDim int) error {
	if emb.ID == "" {
		return fmt.Errorf("embedding ID cannot be empty")
	}
	
	if err := validateVector(emb.Vector); err != nil {
		return fmt.Errorf("invalid vector: %w", err)
	}
	
	if expectedDim > 0 && len(emb.Vector) != expectedDim {
		return fmt.Errorf("vector dimension mismatch: expected %d, got %d", expectedDim, len(emb.Vector))
	}
	
	return nil
}

// validateVector checks if a vector is valid (not nil, not empty, no NaN/Inf values)
func validateVector(vector []float32) error {
	if len(vector) == 0 {
		return ErrInvalidVector
	}
	
	for _, val := range vector {
		if isNaN(float64(val)) || isInf(float64(val), 0) {
			return ErrInvalidVector
		}
	}
	
	return nil
}

// isNaN reports whether f is an IEEE 754 "not-a-number" value
func isNaN(f float64) bool {
	return f != f
}

// isInf reports whether f is an infinity, according to sign
func isInf(f float64, sign int) bool {
	return sign >= 0 && f > 1.7976931348623157e+308 || sign <= 0 && f < -1.7976931348623157e+308
}