package ais

// Tasks 3 & 4: vessel state management and position sanity checks.

import (
	"testing"
	"time"
)

// ---- Task 3: PruneStale, GetActiveVessels, MsgCount -------------------------

func TestPruneStale(t *testing.T) {
	d := NewDecoder()

	now := time.Now()
	d.mu.Lock()
	d.vessels[111] = &Vessel{MMSI: 111, LastSeen: now.Add(-2 * time.Minute)}
	d.vessels[222] = &Vessel{MMSI: 222, LastSeen: now.Add(-10 * time.Minute)}
	d.vessels[333] = &Vessel{MMSI: 333, LastSeen: now.Add(-1 * time.Second)}
	d.mu.Unlock()

	// Prune vessels older than 5 minutes — should remove 222 only.
	removed := d.PruneStale(5 * time.Minute)
	if removed != 1 {
		t.Errorf("PruneStale(5m): got %d removed, want 1", removed)
	}
	vessels := d.GetVessels()
	if _, ok := vessels[222]; ok {
		t.Error("vessel 222 should have been pruned")
	}
	if _, ok := vessels[111]; !ok {
		t.Error("vessel 111 should still be present")
	}
	if _, ok := vessels[333]; !ok {
		t.Error("vessel 333 should still be present")
	}
}

func TestPruneStaleAll(t *testing.T) {
	d := NewDecoder()

	d.mu.Lock()
	d.vessels[1] = &Vessel{MMSI: 1, LastSeen: time.Now().Add(-1 * time.Hour)}
	d.vessels[2] = &Vessel{MMSI: 2, LastSeen: time.Now().Add(-2 * time.Hour)}
	d.mu.Unlock()

	removed := d.PruneStale(30 * time.Minute)
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if len(d.GetVessels()) != 0 {
		t.Error("all vessels should be pruned")
	}
}

func TestPruneStaleNone(t *testing.T) {
	d := NewDecoder()

	d.mu.Lock()
	d.vessels[1] = &Vessel{MMSI: 1, LastSeen: time.Now()}
	d.mu.Unlock()

	removed := d.PruneStale(5 * time.Minute)
	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}
}

func TestGetActiveVessels(t *testing.T) {
	d := NewDecoder()
	now := time.Now()

	d.mu.Lock()
	// Active + valid position
	d.vessels[1] = &Vessel{MMSI: 1, LastSeen: now, PosValid: true, Latitude: 51.0, Longitude: 1.0}
	// Active but no position
	d.vessels[2] = &Vessel{MMSI: 2, LastSeen: now, PosValid: false}
	// Stale but valid position
	d.vessels[3] = &Vessel{MMSI: 3, LastSeen: now.Add(-10 * time.Minute), PosValid: true}
	// Active + valid position
	d.vessels[4] = &Vessel{MMSI: 4, LastSeen: now.Add(-1 * time.Minute), PosValid: true, Latitude: 52.0, Longitude: 2.0}
	d.mu.Unlock()

	active := d.GetActiveVessels(5 * time.Minute)
	if len(active) != 2 {
		t.Errorf("GetActiveVessels: got %d, want 2", len(active))
	}
	for _, v := range active {
		if !v.PosValid {
			t.Errorf("GetActiveVessels returned vessel %d with PosValid=false", v.MMSI)
		}
	}
}

func TestMsgCountIncrement(t *testing.T) {
	d := NewDecoder()

	// Craft a minimal Type 1 position bits with valid lat/lon.
	bits := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, 123456789},
		{50, 10, 10},  // 1.0 kt
		{61, 28, twosComp(int64(1200000), 28)},  // 2.0° lon
		{89, 27, twosComp(int64(30000000), 27)}, // 50.0° lat
		{116, 12, 1800},
		{128, 9, 180},
	})

	for i := 0; i < 3; i++ {
		d.decodePositionClassA(bits)
	}

	vessels := d.GetVessels()
	v := vessels[123456789]
	if v == nil {
		t.Fatal("vessel not found")
	}
	if v.MsgCount != 3 {
		t.Errorf("MsgCount: got %d, want 3", v.MsgCount)
	}
}

func TestMsgCountIncrementType18(t *testing.T) {
	d := NewDecoder()
	bits := makeBits(168, []fieldSpec{
		{0, 6, 18},
		{8, 30, 555000001},
		{46, 10, 5},
		{57, 28, twosComp(int64(600000), 28)},
		{85, 27, twosComp(int64(30600000), 27)},
		{112, 12, 900},
		{124, 9, 90},
	})

	d.decodePositionClassB(bits)
	d.decodePositionClassB(bits)

	v := d.GetVessels()[555000001]
	if v == nil {
		t.Fatal("vessel not found")
	}
	if v.MsgCount != 2 {
		t.Errorf("MsgCount: got %d, want 2", v.MsgCount)
	}
}

// ---- Task 4: Position sanity checks -----------------------------------------

func TestPositionJumpRejected(t *testing.T) {
	d := NewDecoder()

	// First message: establish position at 51.0°N, 0.0°E.
	bits1 := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, 777000001},
		{50, 10, 10},
		{61, 28, twosComp(0, 28)},
		{89, 27, twosComp(int64(30600000), 27)}, // 51.0°
		{116, 12, 0},
		{128, 9, 511},
	})
	d.decodePositionClassA(bits1)

	v := d.GetVessels()[777000001]
	if v == nil || !v.PosValid {
		t.Fatal("first position not established")
	}
	firstLat := v.Latitude
	_ = firstLat

	// Second message within 5 minutes: jump > 0.33° (≈20 NM) should be rejected.
	bits2 := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, 777000001},
		{50, 10, 10},
		{61, 28, twosComp(0, 28)},
		{89, 27, twosComp(int64(33600000), 27)}, // 56.0° — 5° jump, rejected
		{116, 12, 0},
		{128, 9, 511},
	})
	d.decodePositionClassA(bits2)

	v = d.GetVessels()[777000001]
	if absF64(v.Latitude-51.0) > posTol {
		t.Errorf("position jump should be rejected; lat=%.4f, want 51.0", v.Latitude)
	}
}

func TestPositionJumpAllowedAfterGap(t *testing.T) {
	d := NewDecoder()

	const mmsi = uint32(777000002)
	// Establish initial position.
	bits1 := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, uint64(mmsi)},
		{50, 10, 10},
		{61, 28, twosComp(0, 28)},
		{89, 27, twosComp(int64(30600000), 27)}, // 51.0°
		{116, 12, 0},
		{128, 9, 511},
	})
	d.decodePositionClassA(bits1)

	// Backdating LastPosTime to simulate a > 5-minute gap.
	d.mu.Lock()
	d.vessels[mmsi].LastPosTime = time.Now().Add(-6 * time.Minute)
	d.mu.Unlock()

	// Jump to 56.0° — allowed because gap > 5 minutes.
	bits2 := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, uint64(mmsi)},
		{50, 10, 10},
		{61, 28, twosComp(0, 28)},
		{89, 27, twosComp(int64(33600000), 27)}, // 56.0°
		{116, 12, 0},
		{128, 9, 511},
	})
	d.decodePositionClassA(bits2)

	v := d.GetVessels()[mmsi]
	if absF64(v.Latitude-56.0) > posTol {
		t.Errorf("position jump after gap should be accepted; lat=%.4f, want 56.0", v.Latitude)
	}
}

func TestSOGLimitAnyVessel(t *testing.T) {
	// SOG > 102 knots must be rejected for any vessel.
	d := NewDecoder()
	const mmsi = uint32(888000001)

	bits := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, uint64(mmsi)},
		{50, 10, 1030},  // 103.0 knots — over limit
		{61, 28, twosComp(int64(600000), 28)},
		{89, 27, twosComp(int64(30600000), 27)},
		{116, 12, 0},
		{128, 9, 511},
	})
	d.decodePositionClassA(bits)

	v := d.GetVessels()[mmsi]
	if v == nil {
		t.Fatal("vessel not found")
	}
	if v.Speed >= 102.0 {
		t.Errorf("SOG > 102 should be rejected; got %.1f", v.Speed)
	}
}

func TestSOGLimitCargoTanker(t *testing.T) {
	// SOG > 50 knots must be rejected for ship types 60–89.
	d := NewDecoder()
	const mmsi = uint32(888000002)

	// First set ship type via Type 5 static data.
	staticBits := makeBits(424, []fieldSpec{
		{0, 6, 5},
		{8, 30, uint64(mmsi)},
		{232, 8, 70}, // passenger vessel (type 70 = within 60–89)
		{294, 8, 50},
	})
	d.decodeStaticVoyage(staticBits)

	// Now send a position with SOG = 55 kt (over the 50-kt cargo limit).
	bits := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, uint64(mmsi)},
		{50, 10, 550},  // 55.0 knots
		{61, 28, twosComp(int64(600000), 28)},
		{89, 27, twosComp(int64(30600000), 27)},
		{116, 12, 0},
		{128, 9, 511},
	})
	d.decodePositionClassA(bits)

	v := d.GetVessels()[mmsi]
	if v == nil {
		t.Fatal("vessel not found")
	}
	if v.Speed >= 50.0 {
		t.Errorf("SOG > 50 should be rejected for cargo/tanker; got %.1f", v.Speed)
	}
}

func TestSOGValidForFastVessel(t *testing.T) {
	// A non-cargo vessel (ship type 0 / unknown) doing 80 kt should be accepted.
	d := NewDecoder()
	const mmsi = uint32(888000003)

	bits := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, uint64(mmsi)},
		{50, 10, 800}, // 80.0 knots — under 102-kt limit, ship type 0
		{61, 28, twosComp(int64(600000), 28)},
		{89, 27, twosComp(int64(30600000), 27)},
		{116, 12, 0},
		{128, 9, 511},
	})
	d.decodePositionClassA(bits)

	v := d.GetVessels()[mmsi]
	if v == nil {
		t.Fatal("vessel not found")
	}
	if absF64(v.Speed-80.0) > 0.01 {
		t.Errorf("SOG 80 kt should be accepted for non-cargo vessel; got %.1f", v.Speed)
	}
}

func TestPositionJumpRejectedClassB(t *testing.T) {
	d := NewDecoder()
	const mmsi = uint32(999000001)

	// Establish position at 52.0° lat.
	bits1 := makeBits(168, []fieldSpec{
		{0, 6, 18},
		{8, 30, uint64(mmsi)},
		{46, 10, 5},
		{57, 28, twosComp(0, 28)},
		{85, 27, twosComp(int64(31200000), 27)}, // 52.0°
		{112, 12, 0},
		{124, 9, 511},
	})
	d.decodePositionClassB(bits1)

	// Attempt jump to 60.0° within 5 minutes — should be rejected.
	bits2 := makeBits(168, []fieldSpec{
		{0, 6, 18},
		{8, 30, uint64(mmsi)},
		{46, 10, 5},
		{57, 28, twosComp(0, 28)},
		{85, 27, twosComp(int64(36000000), 27)}, // 60.0°
		{112, 12, 0},
		{124, 9, 511},
	})
	d.decodePositionClassB(bits2)

	v := d.GetVessels()[mmsi]
	if v == nil {
		t.Fatal("vessel not found")
	}
	if absF64(v.Latitude-52.0) > posTol {
		t.Errorf("Type 18 position jump should be rejected; lat=%.4f, want 52.0", v.Latitude)
	}
}
