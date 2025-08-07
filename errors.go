package sqvect

import (
	"errors"
	"fmt"
)

// Common errors
var (
	// ErrInvalidDimension is returned when vector dimension doesn't match expected
	ErrInvalidDimension = errors.New("invalid vector dimension")
	
	// ErrNotFound is returned when an embedding is not found
	ErrNotFound = errors.New("embedding not found")
	
	// ErrInvalidVector is returned when vector data is invalid
	ErrInvalidVector = errors.New("invalid vector data")
	
	// ErrStoreClosed is returned when trying to use a closed store
	ErrStoreClosed = errors.New("store is closed")
	
	// ErrInvalidConfig is returned when configuration is invalid
	ErrInvalidConfig = errors.New("invalid configuration")
	
	// ErrEmptyQuery is returned when search query is empty
	ErrEmptyQuery = errors.New("empty query vector")
)

// StoreError wraps errors with operation context
type StoreError struct {
	Op  string // Operation name
	Err error  // Underlying error
}

// Error implements the error interface
func (e *StoreError) Error() string {
	if e.Op == "" {
		return fmt.Sprintf("vectorstore: %v", e.Err)
	}
	return fmt.Sprintf("vectorstore: %s: %v", e.Op, e.Err)
}

// Unwrap returns the underlying error
func (e *StoreError) Unwrap() error {
	return e.Err
}

// Is checks if the error matches the target
func (e *StoreError) Is(target error) bool {
	return errors.Is(e.Err, target)
}

// wrapError wraps an error with operation context
func wrapError(op string, err error) error {
	if err == nil {
		return nil
	}
	return &StoreError{Op: op, Err: err}
}