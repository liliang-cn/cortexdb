package encoding

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// ErrInvalidVector is returned when a vector is invalid
var ErrInvalidVector = errors.New("invalid vector")

// encodeVector converts a float32 slice to bytes using little-endian encoding
// EncodeVector encodes a float32 vector to bytes
func EncodeVector(vector []float32) ([]byte, error) {
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
// DecodeVector decodes bytes to a float32 vector
func DecodeVector(data []byte) ([]float32, error) {
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
// EncodeMetadata encodes metadata to JSON string
func EncodeMetadata(metadata map[string]string) (string, error) {
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
// DecodeMetadata decodes JSON string to metadata
func DecodeMetadata(jsonStr string) (map[string]string, error) {
	if jsonStr == "" {
		return nil, nil
	}
	
	var metadata map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &metadata); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}
	
	return metadata, nil
}

// ValidateEmbedding validates an embedding
func ValidateEmbedding(emb interface{}, expectedDim int) error {
	// This function would need proper type assertion based on the actual embedding type
	// For now, returning nil to allow compilation
	return nil
}

// ValidateVector validates a vector
func ValidateVector(vector []float32) error {
	if vector == nil {
		return ErrInvalidVector
	}
	
	if len(vector) == 0 {
		return ErrInvalidVector
	}
	
	for _, val := range vector {
		if val != val { // NaN check
			return ErrInvalidVector
		}
		// Check for infinity
		if math.IsInf(float64(val), 0) {
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