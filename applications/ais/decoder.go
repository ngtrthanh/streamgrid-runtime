// Package ais implements AIS message decoding from NMEA 0183 sentences.
//
// AIS messages are transmitted as NMEA sentences:
//   !AIVDM,1,1,,B,15MwkT02000000000000000000000,0*4E
//
// Format: !AIVDM,<fragments>,<fragnum>,<seqid>,<channel>,<payload>,<fill>*<checksum>
//
// The payload is 6-bit armored ASCII. This decoder handles:
//   - Message types 1,2,3: Position report (Class A)
//   - Message type 5: Static and voyage related data
//   - Message type 18: Standard Class B position report
//   - Message type 19: Extended Class B position report
//   - Message type 24: Class B static data
package ais

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/streamgrid/streamgrid/generator"
)

// Vessel represents the tracked state of a single AIS vessel.
type Vessel struct {
	MMSI       uint32
	Name       string
	Callsign   string
	IMO        uint32
	ShipType   uint8
	Latitude   float64
	Longitude  float64
	Speed      float64 // knots (SOG)
	Course     float64 // degrees (COG)
	Heading    float64 // degrees (true heading)
	ROT        float64 // rate of turn, degrees/min
	Status     uint8   // navigational status
	LastSeen   time.Time
	PosValid   bool
	Draught    float64 // meters
	Destination string
	DimA, DimB, DimC, DimD uint16 // ship dimensions
}

// Decoder decodes AIS NMEA sentences into vessel state.
type Decoder struct {
	mu          sync.RWMutex
	vessels     map[uint32]*Vessel
	fragments   map[string][]string // multi-sentence message assembly
	onUpdate    func(*Vessel)
	msgCount    uint64
	errCount    uint64
}

// NewDecoder creates a new AIS NMEA decoder.
func NewDecoder() *Decoder {
	return &Decoder{
		vessels:   make(map[uint32]*Vessel),
		fragments: make(map[string][]string),
	}
}

// OnUpdate sets a callback for vessel state updates.
func (d *Decoder) OnUpdate(fn func(*Vessel)) {
	d.onUpdate = fn
}

// FeedLine processes a single NMEA sentence.
// Returns true if a message was successfully decoded.
func (d *Decoder) FeedLine(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) == 0 {
		return false
	}

	// Validate NMEA sentence
	if !strings.HasPrefix(line, "!AIVDM") && !strings.HasPrefix(line, "!AIVDO") {
		return false
	}

	// Verify checksum
	if !verifyChecksum(line) {
		d.errCount++
		return false
	}

	// Parse NMEA fields
	parts := strings.Split(line, ",")
	if len(parts) < 7 {
		d.errCount++
		return false
	}

	totalFrags, _ := strconv.Atoi(parts[1])
	fragNum, _ := strconv.Atoi(parts[2])
	seqID := parts[3]
	// channel := parts[4]
	payload := parts[5]
	// fillBits in parts[6] before *checksum
	fillStr := strings.Split(parts[6], "*")[0]
	fillBits, _ := strconv.Atoi(fillStr)

	// Handle multi-sentence messages
	if totalFrags > 1 {
		key := seqID
		if key == "" {
			key = fmt.Sprintf("_%d", totalFrags)
		}

		if fragNum == 1 {
			d.fragments[key] = []string{payload}
		} else {
			frags, ok := d.fragments[key]
			if !ok {
				d.errCount++
				return false
			}
			d.fragments[key] = append(frags, payload)
		}

		if fragNum < totalFrags {
			return false // Need more fragments
		}

		// Assemble complete message
		frags := d.fragments[key]
		delete(d.fragments, key)
		payload = strings.Join(frags, "")
	}

	// Decode 6-bit armored payload
	bits := decodeSixBit(payload, fillBits)
	if len(bits) < 6 {
		d.errCount++
		return false
	}

	// Get message type
	msgType := bitsToUint(bits, 0, 6)
	
	d.msgCount++

	switch msgType {
	case 1, 2, 3:
		return d.decodePositionClassA(bits)
	case 5:
		return d.decodeStaticVoyage(bits)
	case 18:
		return d.decodePositionClassB(bits)
	case 19:
		return d.decodePositionClassBExtended(bits)
	case 24:
		return d.decodeStaticClassB(bits)
	default:
		return false
	}
}

// decodePositionClassA decodes message types 1, 2, 3.
func (d *Decoder) decodePositionClassA(bits []byte) bool {
	if len(bits) < 168 {
		return false
	}

	mmsi := uint32(bitsToUint(bits, 8, 30))
	status := uint8(bitsToUint(bits, 38, 4))
	rot := bitsToInt(bits, 42, 8)
	sog := bitsToUint(bits, 50, 10)     // 1/10 knot
	posAcc := bitsToUint(bits, 60, 1)
	lonRaw := bitsToInt(bits, 61, 28)    // 1/10000 min
	latRaw := bitsToInt(bits, 89, 27)    // 1/10000 min
	cog := bitsToUint(bits, 116, 12)     // 1/10 degree
	heading := bitsToUint(bits, 128, 9)  // degrees
	_ = posAcc

	// Convert position
	lat := float64(latRaw) / 600000.0
	lon := float64(lonRaw) / 600000.0

	// Validate
	posValid := lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 &&
		latRaw != 0x3412140 && lonRaw != 0x6791AC0 // AIS "not available" values

	d.mu.Lock()
	v, exists := d.vessels[mmsi]
	if !exists {
		v = &Vessel{MMSI: mmsi}
		d.vessels[mmsi] = v
	}
	v.LastSeen = time.Now()
	v.Status = status
	if posValid {
		v.Latitude = lat
		v.Longitude = lon
		v.PosValid = true
	}
	if sog != 1023 { // 1023 = not available
		v.Speed = float64(sog) / 10.0
	}
	if cog != 3600 { // 3600 = not available
		v.Course = float64(cog) / 10.0
	}
	if heading != 511 { // 511 = not available
		v.Heading = float64(heading)
	}
	if rot != -128 { // -128 = not available
		v.ROT = float64(rot)
	}
	d.mu.Unlock()

	if d.onUpdate != nil {
		d.onUpdate(v)
	}
	return true
}

// decodeStaticVoyage decodes message type 5.
func (d *Decoder) decodeStaticVoyage(bits []byte) bool {
	if len(bits) < 424 {
		return false
	}

	mmsi := uint32(bitsToUint(bits, 8, 30))
	imo := uint32(bitsToUint(bits, 40, 30))
	callsign := bitsToString(bits, 70, 42)
	name := bitsToString(bits, 112, 120)
	shipType := uint8(bitsToUint(bits, 232, 8))
	dimA := uint16(bitsToUint(bits, 240, 9))
	dimB := uint16(bitsToUint(bits, 249, 9))
	dimC := uint16(bitsToUint(bits, 258, 6))
	dimD := uint16(bitsToUint(bits, 264, 6))
	draught := float64(bitsToUint(bits, 294, 8)) / 10.0

	var dest string
	if len(bits) >= 404 {
		dest = bitsToString(bits, 302, 120)
	}

	d.mu.Lock()
	v, exists := d.vessels[mmsi]
	if !exists {
		v = &Vessel{MMSI: mmsi}
		d.vessels[mmsi] = v
	}
	v.LastSeen = time.Now()
	v.IMO = imo
	v.Callsign = strings.TrimRight(callsign, "@ ")
	v.Name = strings.TrimRight(name, "@ ")
	v.ShipType = shipType
	v.DimA = dimA
	v.DimB = dimB
	v.DimC = dimC
	v.DimD = dimD
	v.Draught = draught
	v.Destination = strings.TrimRight(dest, "@ ")
	d.mu.Unlock()

	if d.onUpdate != nil {
		d.onUpdate(v)
	}
	return true
}

// decodePositionClassB decodes message type 18.
func (d *Decoder) decodePositionClassB(bits []byte) bool {
	if len(bits) < 168 {
		return false
	}

	mmsi := uint32(bitsToUint(bits, 8, 30))
	sog := bitsToUint(bits, 46, 10)
	lonRaw := bitsToInt(bits, 57, 28)
	latRaw := bitsToInt(bits, 85, 27)
	cog := bitsToUint(bits, 112, 12)
	heading := bitsToUint(bits, 124, 9)

	lat := float64(latRaw) / 600000.0
	lon := float64(lonRaw) / 600000.0
	posValid := lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180

	d.mu.Lock()
	v, exists := d.vessels[mmsi]
	if !exists {
		v = &Vessel{MMSI: mmsi}
		d.vessels[mmsi] = v
	}
	v.LastSeen = time.Now()
	if posValid {
		v.Latitude = lat
		v.Longitude = lon
		v.PosValid = true
	}
	if sog != 1023 {
		v.Speed = float64(sog) / 10.0
	}
	if cog != 3600 {
		v.Course = float64(cog) / 10.0
	}
	if heading != 511 {
		v.Heading = float64(heading)
	}
	d.mu.Unlock()

	if d.onUpdate != nil {
		d.onUpdate(v)
	}
	return true
}

// decodePositionClassBExtended decodes message type 19.
func (d *Decoder) decodePositionClassBExtended(bits []byte) bool {
	if len(bits) < 312 {
		return false
	}

	mmsi := uint32(bitsToUint(bits, 8, 30))
	sog := bitsToUint(bits, 46, 10)
	lonRaw := bitsToInt(bits, 57, 28)
	latRaw := bitsToInt(bits, 85, 27)
	cog := bitsToUint(bits, 112, 12)
	heading := bitsToUint(bits, 124, 9)
	name := bitsToString(bits, 143, 120)
	shipType := uint8(bitsToUint(bits, 263, 8))

	lat := float64(latRaw) / 600000.0
	lon := float64(lonRaw) / 600000.0
	posValid := lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180

	d.mu.Lock()
	v, exists := d.vessels[mmsi]
	if !exists {
		v = &Vessel{MMSI: mmsi}
		d.vessels[mmsi] = v
	}
	v.LastSeen = time.Now()
	v.Name = strings.TrimRight(name, "@ ")
	v.ShipType = shipType
	if posValid {
		v.Latitude = lat
		v.Longitude = lon
		v.PosValid = true
	}
	if sog != 1023 {
		v.Speed = float64(sog) / 10.0
	}
	if cog != 3600 {
		v.Course = float64(cog) / 10.0
	}
	if heading != 511 {
		v.Heading = float64(heading)
	}
	d.mu.Unlock()

	if d.onUpdate != nil {
		d.onUpdate(v)
	}
	return true
}

// decodeStaticClassB decodes message type 24 (Part A and B).
func (d *Decoder) decodeStaticClassB(bits []byte) bool {
	if len(bits) < 160 {
		return false
	}

	mmsi := uint32(bitsToUint(bits, 8, 30))
	partNum := bitsToUint(bits, 38, 2)

	d.mu.Lock()
	v, exists := d.vessels[mmsi]
	if !exists {
		v = &Vessel{MMSI: mmsi}
		d.vessels[mmsi] = v
	}
	v.LastSeen = time.Now()

	switch partNum {
	case 0: // Part A: name
		name := bitsToString(bits, 40, 120)
		v.Name = strings.TrimRight(name, "@ ")
	case 1: // Part B: callsign, type, dimensions
		if len(bits) >= 168 {
			shipType := uint8(bitsToUint(bits, 40, 8))
			callsign := bitsToString(bits, 90, 42)
			v.ShipType = shipType
			v.Callsign = strings.TrimRight(callsign, "@ ")
		}
	}
	d.mu.Unlock()

	if d.onUpdate != nil {
		d.onUpdate(v)
	}
	return true
}

// --- Bit manipulation utilities ---

// decodeSixBit converts AIS 6-bit armored ASCII to a bit array.
func decodeSixBit(payload string, fillBits int) []byte {
	totalBits := len(payload)*6 - fillBits
	if totalBits <= 0 {
		return nil
	}

	bits := make([]byte, totalBits)
	for i, ch := range payload {
		val := int(ch) - 48
		if val > 40 {
			val -= 8
		}
		for b := 5; b >= 0; b-- {
			bitIdx := i*6 + (5 - b)
			if bitIdx < totalBits {
				bits[bitIdx] = byte((val >> b) & 1)
			}
		}
	}
	return bits
}

// bitsToUint extracts an unsigned integer from the bit array.
func bitsToUint(bits []byte, start, length int) uint64 {
	var val uint64
	for i := 0; i < length; i++ {
		if start+i < len(bits) {
			val = (val << 1) | uint64(bits[start+i])
		}
	}
	return val
}

// bitsToInt extracts a signed integer (two's complement) from the bit array.
func bitsToInt(bits []byte, start, length int) int64 {
	val := bitsToUint(bits, start, length)
	if length > 0 && bits[start] == 1 {
		// Negative: sign extend
		val |= ^((1 << length) - 1)
	}
	return int64(val)
}

// bitsToString extracts a 6-bit encoded string from the bit array.
func bitsToString(bits []byte, start, numBits int) string {
	numChars := numBits / 6
	chars := make([]byte, numChars)
	for i := 0; i < numChars; i++ {
		val := bitsToUint(bits, start+i*6, 6)
		if val < 32 {
			chars[i] = byte(val + 64) // '@' through '_'
		} else {
			chars[i] = byte(val) // ' ' through '?'
		}
	}
	return string(chars)
}

// verifyChecksum validates the NMEA XOR checksum.
func verifyChecksum(sentence string) bool {
	starIdx := strings.LastIndex(sentence, "*")
	if starIdx < 0 || starIdx+3 > len(sentence) {
		return false
	}

	// Find start (after ! or $)
	start := 1
	if len(sentence) > 0 && (sentence[0] == '!' || sentence[0] == '$') {
		start = 1
	}

	// XOR all bytes between ! and *
	var checksum byte
	for i := start; i < starIdx; i++ {
		checksum ^= sentence[i]
	}

	expected, err := strconv.ParseUint(sentence[starIdx+1:starIdx+3], 16, 8)
	if err != nil {
		return false
	}

	return checksum == byte(expected)
}

// --- Public API ---

// GetVessels returns a snapshot of all tracked vessels.
func (d *Decoder) GetVessels() map[uint32]*Vessel {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make(map[uint32]*Vessel, len(d.vessels))
	for k, v := range d.vessels {
		cp := *v
		result[k] = &cp
	}
	return result
}

// GetActiveCount returns number of vessels seen within maxAge.
func (d *Decoder) GetActiveCount(maxAge time.Duration) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	cutoff := time.Now().Add(-maxAge)
	count := 0
	for _, v := range d.vessels {
		if v.LastSeen.After(cutoff) {
			count++
		}
	}
	return count
}

// Stats returns decoder statistics.
func (d *Decoder) Stats() (messages, errors uint64) {
	return d.msgCount, d.errCount
}

// ToEntityState converts a Vessel to the canonical EntityState.
func (v *Vessel) ToEntityState(seq uint32) generator.EntityState {
	flags := generator.FlagActive
	if v.PosValid {
		flags |= generator.FlagPositionValid
	}
	if v.Speed > 0 {
		flags |= generator.FlagSpeedValid
	}
	if v.Course > 0 || v.Heading > 0 {
		flags |= generator.FlagHeadingValid
	}

	heading := v.Course
	if v.Heading > 0 && v.Heading < 360 {
		heading = v.Heading
	}

	return generator.EntityState{
		EntityID:    v.MMSI,
		Flags:       flags,
		EntityType:  generator.TypeVessel,
		TimestampMs: uint64(v.LastSeen.UnixMilli()),
		Latitude:    v.Latitude,
		Longitude:   v.Longitude,
		AltitudeM:   0, // Vessels are at sea level
		SpeedMs:     float32(v.Speed) * 0.514444, // knots → m/s
		HeadingDeg:  float32(heading),
		VRateMs:     0,
		Sequence:    seq,
		GridCell:    generator.ComputeGridCell(v.Latitude, v.Longitude, 1.0),
	}
}

// String returns a summary of the vessel.
func (v *Vessel) String() string {
	return fmt.Sprintf("MMSI=%09d name=%q lat=%.4f lon=%.4f sog=%.1fkt cog=%.1f°",
		v.MMSI, v.Name, v.Latitude, v.Longitude, v.Speed, v.Course)
}

// ignore unused import
var _ = math.Pi
