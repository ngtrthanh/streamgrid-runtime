// Package benchmark — Protocol encoding benchmarks.
//
// Compares binary (StreamGrid wire format) vs JSON encoding/decoding
// for 100, 1000, and 10000 entity frames.
//
// Run with:
//
//	go test ./benchmarks/ -bench=Protocol -benchmem
package benchmark

import (
	"encoding/json"
	"testing"

	"github.com/streamgrid/streamgrid/generator"
)

// makeStates creates n EntityState values using the deterministic generator.
func makeStates(n int) []generator.EntityState {
	cfg := generator.DefaultConfig()
	cfg.EntityCount = n
	cfg.Seed = 42
	g := generator.New(cfg)
	return g.Tick()
}

// ---- 100 entities ----

func BenchmarkProtocolBinaryEncode_100(b *testing.B) {
	states := makeStates(100)
	b.SetBytes(int64(100 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.EncodeFrame(states)
	}
}

func BenchmarkProtocolBinaryDecode_100(b *testing.B) {
	states := makeStates(100)
	frame := generator.EncodeFrame(states)
	b.SetBytes(int64(100 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var e generator.EntityState
		for j := 0; j < 100; j++ {
			offset := generator.FrameHeaderSize + j*generator.EntityStateSize
			e.UnmarshalBinary(frame[offset : offset+generator.EntityStateSize])
		}
		_ = e
	}
}

func BenchmarkProtocolJSONEncode_100(b *testing.B) {
	states := makeStates(100)
	b.SetBytes(int64(100 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(states)
	}
}

func BenchmarkProtocolJSONDecode_100(b *testing.B) {
	states := makeStates(100)
	data, _ := json.Marshal(states)
	b.SetBytes(int64(100 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out []generator.EntityState
		_ = json.Unmarshal(data, &out)
	}
}

// ---- 1000 entities ----

func BenchmarkProtocolBinaryEncode_1000(b *testing.B) {
	states := makeStates(1000)
	b.SetBytes(int64(1000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.EncodeFrame(states)
	}
}

func BenchmarkProtocolBinaryDecode_1000(b *testing.B) {
	states := makeStates(1000)
	frame := generator.EncodeFrame(states)
	b.SetBytes(int64(1000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var e generator.EntityState
		for j := 0; j < 1000; j++ {
			offset := generator.FrameHeaderSize + j*generator.EntityStateSize
			e.UnmarshalBinary(frame[offset : offset+generator.EntityStateSize])
		}
		_ = e
	}
}

func BenchmarkProtocolJSONEncode_1000(b *testing.B) {
	states := makeStates(1000)
	b.SetBytes(int64(1000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(states)
	}
}

func BenchmarkProtocolJSONDecode_1000(b *testing.B) {
	states := makeStates(1000)
	data, _ := json.Marshal(states)
	b.SetBytes(int64(1000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out []generator.EntityState
		_ = json.Unmarshal(data, &out)
	}
}

// ---- 10000 entities ----

func BenchmarkProtocolBinaryEncode_10000(b *testing.B) {
	states := makeStates(10000)
	b.SetBytes(int64(10000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.EncodeFrame(states)
	}
}

func BenchmarkProtocolBinaryDecode_10000(b *testing.B) {
	states := makeStates(10000)
	frame := generator.EncodeFrame(states)
	b.SetBytes(int64(10000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var e generator.EntityState
		for j := 0; j < 10000; j++ {
			offset := generator.FrameHeaderSize + j*generator.EntityStateSize
			e.UnmarshalBinary(frame[offset : offset+generator.EntityStateSize])
		}
		_ = e
	}
}

func BenchmarkProtocolJSONEncode_10000(b *testing.B) {
	states := makeStates(10000)
	b.SetBytes(int64(10000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(states)
	}
}

func BenchmarkProtocolJSONDecode_10000(b *testing.B) {
	states := makeStates(10000)
	data, _ := json.Marshal(states)
	b.SetBytes(int64(10000 * generator.EntityStateSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out []generator.EntityState
		_ = json.Unmarshal(data, &out)
	}
}
