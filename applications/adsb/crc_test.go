package adsb

import (
	"encoding/hex"
	"testing"
)

func TestCRC24KnownVectors(t *testing.T) {
	// Known DF17 messages with valid CRC (from real ADS-B captures)
	// Source: various ADS-B test vector collections
	tests := []struct {
		name string
		hex  string
		valid bool
	}{
		// DF17 airborne position messages (CRC should be valid)
		{"DF17_position_1", "8D40621D58C382D690C8AC2863A7", true},
		{"DF17_position_2", "8D485020994409940838175B284F", true},
		{"DF17_ident", "8D4840D6202CC371C32CE0576098", true},
		{"DF17_velocity", "8D406B902015A678D4D220AA4BDA", true},
		// Invalid (corrupted last byte)
		{"corrupted", "8D40621D58C382D690C8AC2863A6", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := hex.DecodeString(tt.hex)
			if err != nil {
				t.Fatalf("invalid hex: %v", err)
			}

			valid, remainder := ValidateCRC(msg)
			if valid != tt.valid {
				t.Errorf("expected valid=%v, got valid=%v (remainder=0x%06X)", tt.valid, valid, remainder)
			}
			if tt.valid && remainder != 0 {
				t.Errorf("valid message should have remainder 0, got 0x%06X", remainder)
			}
		})
	}
}

func TestCRC24Table(t *testing.T) {
	// Verify table initialization - first few entries
	if crc24Table[0] != 0 {
		t.Errorf("crc24Table[0] should be 0, got 0x%06X", crc24Table[0])
	}
	// Table should have non-zero entries
	nonZero := 0
	for _, v := range crc24Table {
		if v != 0 {
			nonZero++
		}
	}
	if nonZero < 200 {
		t.Errorf("CRC table has too few non-zero entries: %d", nonZero)
	}
}

func TestComputeCRC24(t *testing.T) {
	// For a valid DF17 message, CRC of first 11 bytes should equal last 3 bytes
	msg, _ := hex.DecodeString("8D40621D58C382D690C8AC2863A7")
	if len(msg) != 14 {
		t.Fatalf("expected 14 bytes, got %d", len(msg))
	}

	crc := ComputeCRC24(msg[:11])
	expected := uint32(msg[11])<<16 | uint32(msg[12])<<8 | uint32(msg[13])
	if crc != expected {
		t.Errorf("CRC mismatch: computed=0x%06X, expected=0x%06X", crc, expected)
	}
}

func BenchmarkCRC24(b *testing.B) {
	msg, _ := hex.DecodeString("8D40621D58C382D690C8AC2863A7")
	b.SetBytes(14)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeCRC24(msg)
	}
}
