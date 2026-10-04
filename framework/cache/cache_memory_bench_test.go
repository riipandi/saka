package cache

import (
	"testing"
	"time"
)

// The benchmarks pin the properties the in-memory driver is designed
// around: a Get with room in dst, a same-length overwrite, and a write into
// an already-grown ring must not allocate, and the sharded locks must let
// parallel readers through.

func BenchmarkMemoryGetHit(b *testing.B) {
	c := NewMemory(0, 5*time.Minute)
	c.Set(b.Context(), "bench:key", make([]byte, 256), 0)

	dst := make([]byte, 0, 512)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		value, ok := c.Get(b.Context(), dst, "bench:key")
		if !ok || len(value) != 256 {
			b.Fatal("unexpected cache answer")
		}
	}
}

func BenchmarkMemoryGetHitParallel(b *testing.B) {
	c := NewMemory(0, 5*time.Minute)
	c.Set(b.Context(), "bench:key", make([]byte, 256), 0)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		dst := make([]byte, 0, 512)
		for pb.Next() {
			value, ok := c.Get(b.Context(), dst, "bench:key")
			if !ok || len(value) != 256 {
				b.Error("unexpected cache answer")
			}
		}
	})
}

func BenchmarkMemoryGetMiss(b *testing.B) {
	c := NewMemory(0, 5*time.Minute)

	b.ReportAllocs()
	for b.Loop() {
		if _, ok := c.Get(b.Context(), nil, "bench:absent"); ok {
			b.Fatal("unexpected hit")
		}
	}
}

func BenchmarkMemorySetOverwrite(b *testing.B) {
	// A same-length overwrite lands in place: the entry keeps its ring
	// position and only the bytes are rewritten.
	c := NewMemory(0, 5*time.Minute)
	value := make([]byte, 256)
	c.Set(b.Context(), "bench:key", value, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.Set(b.Context(), "bench:key", value, 0)
	}
}

func BenchmarkMemorySetNewKey(b *testing.B) {
	c := NewMemory(0, 5*time.Minute)
	value := make([]byte, 256)
	keys := make([]string, 256)
	for i := range keys {
		keys[i] = "bench:key-" + string(rune('a'+i%26)) + string(rune('a'+i/26%26))
	}

	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		c.Set(b.Context(), keys[i%len(keys)], value, 0)
	}
}

func BenchmarkMemoryBatchGet(b *testing.B) {
	c := NewMemory(0, 5*time.Minute)
	items := make(map[string][]byte, 16)
	for i := range 16 {
		items["bench:key-"+string(rune('a'+i))] = make([]byte, 64)
	}
	c.SetMany(b.Context(), items, 0)
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if found := c.GetMany(b.Context(), keys); len(found) != len(keys) {
			b.Fatal("unexpected batch answer")
		}
	}
}
