package quantization

import (
	"math/bits"
)

// Binary codes: one bit per dimension, packed into uint64 words.
//
// A 768-dimension float32 vector is 3072 bytes; its sign code is 96 — 1/32 of
// it — and the Hamming distance between two codes is twelve XORs and twelve
// popcounts (math/bits.OnesCount64 compiles to the POPCNT / CNT instruction).
// For vectors whose angle is what matters, the fraction of sign bits two
// vectors disagree on estimates that angle (Charikar's SimHash argument, with
// the coordinate axes standing in for random hyperplanes), so Hamming
// distance ranks neighbours roughly as cosine distance does — roughly, which
// is why a binary search over-fetches and rescores the shortlist with the
// full vectors.
//
// Centering. Sign quantization asks of each coordinate "above or below
// zero?", which only splits the data when the data straddles zero. Learned
// embeddings usually do not: most dimensions have a mean offset, some large,
// and a dimension whose values are all positive puts the same bit in every
// code and contributes nothing but noise to the distance. Subtracting the
// per-dimension mean first makes each bit split the data near its median and
// carry information. Cosine over the original vectors is not cosine over the
// centered ones, but neighbours under one are mostly neighbours under the
// other, and the rescoring step uses the originals anyway.

// BinaryWords is the number of uint64 words a dim-bit code occupies.
func BinaryWords(dim int) int { return (dim + 63) / 64 }

// PackSigns writes v's code into dst (len BinaryWords(len(v))): bit d is set
// when v[d] > center[d], or v[d] > 0 when center is nil. Trailing bits of the
// last word stay zero in every code, so they never add to a distance.
func PackSigns(dst []uint64, v []float32, center []float32) {
	for i := range dst {
		dst[i] = 0
	}
	if center == nil {
		for d, x := range v {
			if x > 0 {
				dst[d>>6] |= 1 << uint(d&63)
			}
		}
		return
	}
	for d, x := range v {
		if x > center[d] {
			dst[d>>6] |= 1 << uint(d&63)
		}
	}
}

// HammingWords counts the bits on which two codes differ.
func HammingWords(a, b []uint64) int {
	n := 0
	for i := range a {
		n += bits.OnesCount64(a[i] ^ b[i])
	}
	return n
}

// MeanVector is the per-dimension mean of vectors, the center PackSigns
// subtracts. Accumulated in float64: a float32 running sum over 100k
// embeddings loses the low digits that the mean of a near-zero dimension is
// made of.
func MeanVector(vectors [][]float32, dim int) []float32 {
	if len(vectors) == 0 {
		return nil
	}
	sum := make([]float64, dim)
	for _, v := range vectors {
		for d := 0; d < dim && d < len(v); d++ {
			sum[d] += float64(v[d])
		}
	}
	mean := make([]float32, dim)
	for d := range sum {
		mean[d] = float32(sum[d] / float64(len(vectors)))
	}
	return mean
}
