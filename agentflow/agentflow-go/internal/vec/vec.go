// Package vec is a tiny shared helper for L2-normalized cosine math used
// by the embedding router and the dense-RAG retriever. Kept separate so
// neither importer needs to depend on the other.
package vec

import "math"

// Dot returns the dot product of two equal-length float32 vectors as a
// float64. With L2-normalized inputs, this equals cosine similarity.
func Dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// Normalize returns a copy of v scaled to unit L2 length. Returns the
// input unchanged if its norm is zero.
func Normalize(v []float32) []float32 {
	var ss float64
	for _, x := range v {
		ss += float64(x) * float64(x)
	}
	if ss == 0 {
		return v
	}
	inv := float32(1.0 / math.Sqrt(ss))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}
