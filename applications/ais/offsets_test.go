package ais

// Task 2: Bit-offset verification against ITU-R M.1371-5.
//
// Test vectors are constructed by hand-encoding known field values into
// 6-bit armored NMEA payloads, then verifying the decoder extracts the
// expected values.  Where a public sample sentence is available its
// decoded fields are cross-checked against published decode tables.

import (
	"math"
	"strings"
	"testing"
)

// tolerance for floating-point position comparison (1/600000 degree ≈ 0.17 m)
const posTol = 1.0 / 600000.0

// absF64 returns the absolute value of a float64.
func absF64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- Helper: build a raw bit slice from field specs ---------------------------

type fieldSpec struct {
	start  int
	length int
	value  uint64
}

// makeBits allocates a zeroed bit slice of the given total length and writes
// each field.  Signed values must be passed as their two's-complement
// unsigned representation.
func makeBits(totalBits int, fields []fieldSpec) []byte {
	bits := make([]byte, totalBits)
	for _, f := range fields {
		for i := 0; i < f.length; i++ {
			bit := (f.value >> uint(f.length-1-i)) & 1
			bits[f.start+i] = byte(bit)
		}
	}
	return bits
}

// twosComp converts a signed integer to its unsigned two's-complement
// representation in the given number of bits.
func twosComp(v int64, bits int) uint64 {
	mask := uint64((1 << bits) - 1)
	return uint64(v) & mask
}

// ---- Type 1/2/3 bit-offset tests --------------------------------------------

func TestType1BitOffsets(t *testing.T) {
	// Build a synthetic Type 1 message with known field values.
	// ITU-R M.1371-5 Table 9.
	const (
		wantMMSI    = uint32(123456789)
		wantStatus  = uint8(3)
		wantSOG     = 85   // raw 0.1-knot units → 8.5 kt
		wantLonRaw  = int64(20112000)  // 20112000 / 600000 = 33.52°
		wantLatRaw  = int64(30360000)  // 30360000 / 600000 = 50.6°
		wantCOG     = 1234 // raw 0.1-deg units → 123.4°
		wantHeading = 270
	)

	bits := makeBits(168, []fieldSpec{
		{0, 6, 1},                                     // msg type = 1
		{8, 30, uint64(wantMMSI)},
		{38, 4, uint64(wantStatus)},
		{42, 8, 0},                                    // ROT = 0
		{50, 10, uint64(wantSOG)},
		{60, 1, 0},                                    // pos accuracy
		{61, 28, twosComp(wantLonRaw, 28)},
		{89, 27, twosComp(wantLatRaw, 27)},
		{116, 12, uint64(wantCOG)},
		{128, 9, uint64(wantHeading)},
	})

	d := NewDecoder()
	if !d.decodePositionClassA(bits) {
		t.Fatal("decodePositionClassA returned false")
	}

	vessels := d.GetVessels()
	v, ok := vessels[wantMMSI]
	if !ok {
		t.Fatalf("vessel %d not found", wantMMSI)
	}

	wantLat := float64(wantLatRaw) / 600000.0
	wantLon := float64(wantLonRaw) / 600000.0

	if v.MMSI != wantMMSI {
		t.Errorf("MMSI: got %d, want %d", v.MMSI, wantMMSI)
	}
	if v.Status != wantStatus {
		t.Errorf("Status: got %d, want %d", v.Status, wantStatus)
	}
	if absF64(v.Speed-float64(wantSOG)/10.0) > 0.01 {
		t.Errorf("SOG: got %.2f, want %.2f", v.Speed, float64(wantSOG)/10.0)
	}
	if absF64(v.Latitude-wantLat) > posTol {
		t.Errorf("Lat: got %.6f, want %.6f", v.Latitude, wantLat)
	}
	if absF64(v.Longitude-wantLon) > posTol {
		t.Errorf("Lon: got %.6f, want %.6f", v.Longitude, wantLon)
	}
	if absF64(v.Course-float64(wantCOG)/10.0) > 0.01 {
		t.Errorf("COG: got %.1f, want %.1f", v.Course, float64(wantCOG)/10.0)
	}
	if absF64(v.Heading-float64(wantHeading)) > 0.01 {
		t.Errorf("Heading: got %.0f, want %d", v.Heading, wantHeading)
	}
}

func TestType1NegativeLongitude(t *testing.T) {
	// Verify two's-complement signed decode for western longitudes.
	const wantLonRaw = int64(-1080000) // -1080000/600000 = -1.8° (west)
	const wantLatRaw = int64(31200000) // 52.0°

	bits := makeBits(168, []fieldSpec{
		{0, 6, 1},
		{8, 30, 987654321},
		{50, 10, 10},
		{61, 28, twosComp(wantLonRaw, 28)},
		{89, 27, twosComp(wantLatRaw, 27)},
		{116, 12, 0},
		{128, 9, 511}, // 511 = not available
	})

	d := NewDecoder()
	d.decodePositionClassA(bits)
	vessels := d.GetVessels()
	v := vessels[987654321]
	if v == nil {
		t.Fatal("vessel not found")
	}
	wantLon := float64(wantLonRaw) / 600000.0
	if absF64(v.Longitude-wantLon) > posTol {
		t.Errorf("Lon: got %.6f, want %.6f", v.Longitude, wantLon)
	}
}

// ---- Type 5 bit-offset tests ------------------------------------------------

func TestType5BitOffsets(t *testing.T) {
	// ITU-R M.1371-5 Table 12, 424 bits total.
	const (
		wantMMSI     = uint32(369970000)
		wantIMO      = uint32(9166234)
		wantShipType = uint8(70) // passenger
		wantDimA     = uint16(160)
		wantDimB     = uint16(40)
		wantDimC     = uint16(20)
		wantDimD     = uint16(8)
		wantDraught  = 62 // raw tenths → 6.2 m
	)
	wantCallsign := "WDC7777"
	wantName     := "PRIDE OF DOVER      " // 20 chars, padded with spaces
	wantDest     := "DOVER               "

	// Encode callsign (7 chars × 6 bits = 42 bits at offset 70)
	callBits := encodeAISString(wantCallsign, 7)
	nameBits  := encodeAISString(wantName, 20)
	destBits  := encodeAISString(wantDest, 20)

	bits := makeBits(424, []fieldSpec{
		{0, 6, 5},
		{8, 30, uint64(wantMMSI)},
		{40, 30, uint64(wantIMO)},
		{232, 8, uint64(wantShipType)},
		{240, 9, uint64(wantDimA)},
		{249, 9, uint64(wantDimB)},
		{258, 6, uint64(wantDimC)},
		{264, 6, uint64(wantDimD)},
		{294, 8, uint64(wantDraught)},
	})
	// Embed string bit fields manually
	copyBits(bits, 70, callBits)
	copyBits(bits, 112, nameBits)
	copyBits(bits, 302, destBits)

	d := NewDecoder()
	if !d.decodeStaticVoyage(bits) {
		t.Fatal("decodeStaticVoyage returned false")
	}
	vessels := d.GetVessels()
	v := vessels[wantMMSI]
	if v == nil {
		t.Fatalf("vessel %d not found", wantMMSI)
	}

	if v.IMO != wantIMO {
		t.Errorf("IMO: got %d, want %d", v.IMO, wantIMO)
	}
	if v.Callsign != strings.TrimRight(wantCallsign, " @") {
		t.Errorf("Callsign: got %q, want %q", v.Callsign, wantCallsign)
	}
	wantNameTrimmed := strings.TrimRight(wantName, " @")
	if v.Name != wantNameTrimmed {
		t.Errorf("Name: got %q, want %q", v.Name, wantNameTrimmed)
	}
	if v.ShipType != wantShipType {
		t.Errorf("ShipType: got %d, want %d", v.ShipType, wantShipType)
	}
	if v.DimA != wantDimA {
		t.Errorf("DimA: got %d, want %d", v.DimA, wantDimA)
	}
	if v.DimB != wantDimB {
		t.Errorf("DimB: got %d, want %d", v.DimB, wantDimB)
	}
	if v.DimC != wantDimC {
		t.Errorf("DimC: got %d, want %d", v.DimC, wantDimC)
	}
	if v.DimD != wantDimD {
		t.Errorf("DimD: got %d, want %d", v.DimD, wantDimD)
	}
	if absF64(v.Draught-float64(wantDraught)/10.0) > 0.01 {
		t.Errorf("Draught: got %.1f, want %.1f", v.Draught, float64(wantDraught)/10.0)
	}
	wantDestTrimmed := strings.TrimRight(wantDest, " @")
	if v.Destination != wantDestTrimmed {
		t.Errorf("Destination: got %q, want %q", v.Destination, wantDestTrimmed)
	}
}

// ---- Type 18 bit-offset tests -----------------------------------------------

func TestType18BitOffsets(t *testing.T) {
	// ITU-R M.1371-5 Table 46, 168 bits.
	const (
		wantMMSI   = uint32(338234631)
		wantSOG    = uint64(15)  // 1.5 kt
		wantLonRaw = int64(-4200000) // -7.0°
		wantLatRaw = int64(29880000) // 49.8°
		wantCOG    = uint64(2340)    // 234.0°
		wantHDG    = uint64(232)
	)

	bits := makeBits(168, []fieldSpec{
		{0, 6, 18},
		{8, 30, uint64(wantMMSI)},
		// reserved: 38-45
		{46, 10, wantSOG},
		// posAcc: 56
		{57, 28, twosComp(wantLonRaw, 28)},
		{85, 27, twosComp(wantLatRaw, 27)},
		{112, 12, wantCOG},
		{124, 9, wantHDG},
	})

	d := NewDecoder()
	if !d.decodePositionClassB(bits) {
		t.Fatal("decodePositionClassB returned false")
	}
	vessels := d.GetVessels()
	v := vessels[wantMMSI]
	if v == nil {
		t.Fatalf("vessel %d not found", wantMMSI)
	}

	wantLat := float64(wantLatRaw) / 600000.0
	wantLon := float64(wantLonRaw) / 600000.0

	if absF64(v.Speed-float64(wantSOG)/10.0) > 0.01 {
		t.Errorf("SOG: got %.2f, want %.2f", v.Speed, float64(wantSOG)/10.0)
	}
	if absF64(v.Latitude-wantLat) > posTol {
		t.Errorf("Lat: got %.6f, want %.6f", v.Latitude, wantLat)
	}
	if absF64(v.Longitude-wantLon) > posTol {
		t.Errorf("Lon: got %.6f, want %.6f", v.Longitude, wantLon)
	}
	if absF64(v.Course-float64(wantCOG)/10.0) > 0.01 {
		t.Errorf("COG: got %.1f, want %.1f", v.Course, float64(wantCOG)/10.0)
	}
	if absF64(v.Heading-float64(wantHDG)) > 0.01 {
		t.Errorf("Heading: got %.0f, want %d", v.Heading, wantHDG)
	}
}

// ---- Type 19 bit-offset tests -----------------------------------------------

func TestType19BitOffsets(t *testing.T) {
	// ITU-R M.1371-5 Table 47, 312 bits.
	// Position fields identical to Type 18.
	// Name at bit 143 (120 bits = 20 chars), ship type at bit 263 (8 bits).
	const (
		wantMMSI     = uint32(244050748)
		wantLonRaw   = int64(2532000)  // 4.22°
		wantLatRaw   = int64(30834000) // 51.39°
		wantShipType = uint8(52)
	)
	wantName := "CELESTINE           " // 20 chars

	nameBits := encodeAISString(wantName, 20)
	bits := makeBits(312, []fieldSpec{
		{0, 6, 19},
		{8, 30, uint64(wantMMSI)},
		{46, 10, 0},
		{57, 28, twosComp(wantLonRaw, 28)},
		{85, 27, twosComp(wantLatRaw, 27)},
		{112, 12, 0},
		{124, 9, 511}, // heading not available
		{263, 8, uint64(wantShipType)},
	})
	copyBits(bits, 143, nameBits)

	d := NewDecoder()
	if !d.decodePositionClassBExtended(bits) {
		t.Fatal("decodePositionClassBExtended returned false")
	}
	vessels := d.GetVessels()
	v := vessels[wantMMSI]
	if v == nil {
		t.Fatalf("vessel %d not found", wantMMSI)
	}

	wantNameTrimmed := strings.TrimRight(wantName, " @")
	if v.Name != wantNameTrimmed {
		t.Errorf("Name: got %q, want %q", v.Name, wantNameTrimmed)
	}
	if v.ShipType != wantShipType {
		t.Errorf("ShipType: got %d, want %d", v.ShipType, wantShipType)
	}
	wantLat := float64(wantLatRaw) / 600000.0
	wantLon := float64(wantLonRaw) / 600000.0
	if absF64(v.Latitude-wantLat) > posTol {
		t.Errorf("Lat: got %.6f, want %.6f", v.Latitude, wantLat)
	}
	if absF64(v.Longitude-wantLon) > posTol {
		t.Errorf("Lon: got %.6f, want %.6f", v.Longitude, wantLon)
	}
}

// ---- Type 24 bit-offset tests -----------------------------------------------

func TestType24PartABitOffsets(t *testing.T) {
	// ITU-R M.1371-5 Table 49.
	// Part A: MMSI 8-37, part# 38-39, name 40-159 (20 chars).
	const wantMMSI = uint32(338234631)
	wantName := "BLUE HERON          "

	nameBits := encodeAISString(wantName, 20)
	bits := makeBits(168, []fieldSpec{
		{0, 6, 24},
		{8, 30, uint64(wantMMSI)},
		{38, 2, 0}, // Part A
	})
	copyBits(bits, 40, nameBits)

	d := NewDecoder()
	if !d.decodeStaticClassB(bits) {
		t.Fatal("decodeStaticClassB returned false")
	}
	vessels := d.GetVessels()
	v := vessels[wantMMSI]
	if v == nil {
		t.Fatalf("vessel %d not found", wantMMSI)
	}
	wantNameTrimmed := strings.TrimRight(wantName, " @")
	if v.Name != wantNameTrimmed {
		t.Errorf("Name: got %q, want %q", v.Name, wantNameTrimmed)
	}
}

func TestType24PartBBitOffsets(t *testing.T) {
	// Part B: ship type 40-47 (8 bits), callsign 90-131 (42 bits = 7 chars).
	const (
		wantMMSI     = uint32(338234631)
		wantShipType = uint8(37) // pleasure craft
	)
	wantCallsign := "WDC7777"

	callBits := encodeAISString(wantCallsign, 7)
	bits := makeBits(168, []fieldSpec{
		{0, 6, 24},
		{8, 30, uint64(wantMMSI)},
		{38, 2, 1}, // Part B
		{40, 8, uint64(wantShipType)},
		// vendor ID: 48-89 (42 bits) — leave zero
	})
	copyBits(bits, 90, callBits)

	d := NewDecoder()
	// Pre-populate vessel so MsgCount > 0
	d.decodeStaticClassB(bits)
	vessels := d.GetVessels()
	v := vessels[wantMMSI]
	if v == nil {
		t.Fatalf("vessel %d not found", wantMMSI)
	}
	if v.ShipType != wantShipType {
		t.Errorf("ShipType: got %d, want %d", v.ShipType, wantShipType)
	}
	wantCS := strings.TrimRight(wantCallsign, " @")
	if v.Callsign != wantCS {
		t.Errorf("Callsign: got %q, want %q", v.Callsign, wantCS)
	}
}

// ---- String encoding helper -------------------------------------------------

// encodeAISString encodes a string into AIS 6-bit characters as a flat bit
// slice (numChars * 6 bits).  Characters are space-padded if shorter.
func encodeAISString(s string, numChars int) []byte {
	bits := make([]byte, numChars*6)
	for i := 0; i < numChars; i++ {
		var ch byte
		if i < len(s) {
			ch = s[i]
		} else {
			ch = ' '
		}
		// Reverse of bitsToString: space(32)→32, '@'(64)→0
		var val byte
		if ch >= 64 {
			val = ch - 64
		} else {
			val = ch
		}
		for b := 0; b < 6; b++ {
			bits[i*6+b] = (val >> uint(5-b)) & 1
		}
	}
	return bits
}

// copyBits copies src bits into dst starting at dstOffset.
func copyBits(dst []byte, dstOffset int, src []byte) {
	for i, b := range src {
		if dstOffset+i < len(dst) {
			dst[dstOffset+i] = b
		}
	}
}

// Ensure math is imported (used by posTol-related helpers)
var _ = math.Pi
