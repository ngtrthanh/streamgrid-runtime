package generator

import (
	"math"
	"testing"
)

func TestNewGenerator(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 100
	g := New(cfg)

	if g == nil {
		t.Fatal("generator is nil")
	}
	if len(g.entities) != 100 {
		t.Fatalf("expected 100 entities, got %d", len(g.entities))
	}
}

func TestTickProducesEntities(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 50
	g := New(cfg)

	states := g.Tick()
	if len(states) != 50 {
		t.Fatalf("expected 50 states, got %d", len(states))
	}

	for i, s := range states {
		if s.EntityID == 0 {
			t.Errorf("entity %d has zero ID", i)
		}
		if s.Flags&FlagActive == 0 {
			t.Errorf("entity %d is not active", i)
		}
		if s.Flags&FlagPositionValid == 0 {
			t.Errorf("entity %d has invalid position", i)
		}
		if s.Latitude < -90 || s.Latitude > 90 {
			t.Errorf("entity %d has invalid latitude: %f", i, s.Latitude)
		}
		if s.Longitude < -180 || s.Longitude > 180 {
			t.Errorf("entity %d has invalid longitude: %f", i, s.Longitude)
		}
		if s.TimestampMs == 0 {
			t.Errorf("entity %d has zero timestamp", i)
		}
		if s.Sequence == 0 {
			t.Errorf("entity %d has zero sequence", i)
		}
	}
}

func TestTickIntoZeroAlloc(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 100
	g := New(cfg)

	states := make([]EntityState, 100)
	n := g.TickInto(states)
	if n != 100 {
		t.Fatalf("expected 100, got %d", n)
	}

	if states[0].EntityID == 0 {
		t.Error("first entity has zero ID")
	}
}

func TestEntitiesMove(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 10
	g := New(cfg)

	states1 := g.Tick()
	states2 := g.Tick()

	movedCount := 0
	for i := range states1 {
		if states1[i].EntityType == TypeSensor {
			continue // Stationary
		}
		if states1[i].Latitude != states2[i].Latitude || states1[i].Longitude != states2[i].Longitude {
			movedCount++
		}
	}

	if movedCount == 0 {
		t.Error("no entities moved between ticks")
	}
}

func TestDeterministicWithSameSeed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 20
	cfg.Seed = 123

	g1 := New(cfg)
	g2 := New(cfg)

	states1 := g1.Tick()
	states2 := g2.Tick()

	for i := range states1 {
		if states1[i].Latitude != states2[i].Latitude {
			t.Errorf("entity %d: positions differ with same seed: %f vs %f",
				i, states1[i].Latitude, states2[i].Latitude)
			break
		}
	}
}

func TestSequenceIncrements(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 5
	g := New(cfg)

	s1 := g.Tick()
	s2 := g.Tick()

	if s1[0].Sequence >= s2[0].Sequence {
		t.Errorf("sequence did not increment: %d >= %d", s1[0].Sequence, s2[0].Sequence)
	}
}

func TestEncodeFrame(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 10
	g := New(cfg)

	states := g.Tick()
	frame := EncodeFrame(states)

	expectedSize := FrameHeaderSize + 10*EntityStateSize
	if len(frame) != expectedSize {
		t.Fatalf("expected frame size %d, got %d", expectedSize, len(frame))
	}

	// Verify magic
	magic := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16 | uint32(frame[3])<<24
	if magic != FrameMagic {
		t.Errorf("expected magic 0x%X, got 0x%X", FrameMagic, magic)
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EntityCount = 5
	g := New(cfg)

	original := g.Tick()
	frame := EncodeFrame(original)

	// Decode entities from frame
	decoded := make([]EntityState, 5)
	for i := 0; i < 5; i++ {
		offset := FrameHeaderSize + i*EntityStateSize
		decoded[i].UnmarshalBinary(frame[offset : offset+EntityStateSize])
	}

	for i := range original {
		if original[i].EntityID != decoded[i].EntityID {
			t.Errorf("entity %d ID mismatch: %d vs %d", i, original[i].EntityID, decoded[i].EntityID)
		}
		if original[i].Latitude != decoded[i].Latitude {
			t.Errorf("entity %d lat mismatch: %f vs %f", i, original[i].Latitude, decoded[i].Latitude)
		}
		if original[i].Longitude != decoded[i].Longitude {
			t.Errorf("entity %d lon mismatch: %f vs %f", i, original[i].Longitude, decoded[i].Longitude)
		}
	}
}

func TestComputeGridCell(t *testing.T) {
	// London at 1-degree cells
	cell := ComputeGridCell(51.5, -0.1, 1.0)
	expectedX := uint32(math.Floor((-0.1 + 180.0) / 1.0)) // 179
	expectedY := uint32(math.Floor((51.5 + 90.0) / 1.0))  // 141
	expected := expectedY*360 + expectedX
	if cell != expected {
		t.Errorf("grid cell mismatch: got %d, expected %d", cell, expected)
	}
}

// Benchmarks

func BenchmarkTick100(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EntityCount = 100
	g := New(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Tick()
	}
}

func BenchmarkTick1000(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EntityCount = 1000
	g := New(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Tick()
	}
}

func BenchmarkTick10000(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EntityCount = 10000
	g := New(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Tick()
	}
}

func BenchmarkTick100000(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EntityCount = 100000
	g := New(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Tick()
	}
}

func BenchmarkTickInto100000(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EntityCount = 100000
	g := New(cfg)
	states := make([]EntityState, 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.TickInto(states)
	}
}

func BenchmarkEncodeFrame10000(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EntityCount = 10000
	g := New(cfg)
	states := g.Tick()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeFrame(states)
	}
}

func BenchmarkEncodeFrameInto10000(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EntityCount = 10000
	g := New(cfg)
	states := g.Tick()
	buf := make([]byte, FrameHeaderSize+10000*EntityStateSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeFrameInto(states, buf)
	}
}
