package maphash

import "unsafe"

// Hasher hashes values of type K.
// Uses runtime AES-based hashing.
type Hasher[K comparable] struct {
	hash hashfn
	seed uintptr
}

// NewHasher creates a new Hasher[K] with a random seed.
func NewHasher[K comparable]() Hasher[K] {
	return Hasher[K]{
		hash: getRuntimeHasher[K](),
		seed: newHashSeed(),
	}
}

// NewSeed returns a copy of |h| with a new hash seed.
func NewSeed[K comparable](h Hasher[K]) Hasher[K] {
	return Hasher[K]{
		hash: h.hash,
		seed: newHashSeed(),
	}
}

// Hash hashes |key|.
func (h Hasher[K]) Hash(key K) uint64 {
	return uint64(h.Hash2(key))
}

// Hash2 hashes |key| as more flexible uintptr.
func (h Hasher[K]) Hash2(key K) uintptr {
	// promise to the compiler that pointer
	// |p| does not escape the stack.
	p := noescape(unsafe.Pointer(&key))
	return h.hash(p, h.seed)
}
