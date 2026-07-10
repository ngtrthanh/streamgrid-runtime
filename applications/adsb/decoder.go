// Package adsb implements ADS-B message decoding from Beast binary format.
//
// Beast format (used by dump1090, Mode-S Beast, etc.):
//   0x1A = escape byte
//   Message format: <0x1A> <type> <timestamp 6B> <signal 1B> <payload>
//   Type 1: Mode-AC (2 bytes payload)
//   Type 2: Mode-S short (7 bytes payload)
//   Type 3: Mode-S long (14 bytes payload) — ADS-B lives here
//   Type 4: Status (unused)
//   Escaped 0x1A in data is doubled: 0x1A 0x1A
//
// This decoder focuses on DF17 (Extended Squitter) which carries ADS-B:
//   - TC 1-4: Aircraft identification
//   - TC 9-18: Airborne position (CPR encoding)
//   - TC 19: Airborne velocity
//   - TC 20-22: Airborne position (GNSS altitude)
package adsb

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/streamgrid/streamgrid/generator"
)

// Aircraft represents the tracked state of a single aircraft.
type Aircraft struct {
	ICAO       uint32
	Callsign   string
	Latitude   float64
	Longitude  float64
	Altitude   int32 // feet
	Speed      float64 // knots
	Heading    float64 // degrees
	VRate      int32 // feet/min
	LastSeen   time.Time
	PosValid   bool
	HasOddCPR  bool
	HasEvenCPR bool
	OddLat     float64
	OddLon     float64
	EvenLat    float64
	EvenLon    float64
	OddTime   time.Time
	EvenTime  time.Time
	Category   uint8
	Squawk     uint16
}

// Decoder decodes Beast-format binary streams into aircraft state.
type Decoder struct {
	mu        sync.RWMutex
	aircraft  map[uint32]*Aircraft
	buf       []byte
	onUpdate  func(*Aircraft)
	msgCount  uint64
	errCount  uint64
}

// NewDecoder creates a new Beast format decoder.
func NewDecoder() *Decoder {
	return &Decoder{
		aircraft: make(map[uint32]*Aircraft),
		buf:      make([]byte, 0, 4096),
	}
}

// OnUpdate sets a callback for aircraft state updates.
func (d *Decoder) OnUpdate(fn func(*Aircraft)) {
	d.onUpdate = fn
}

// Feed pushes raw bytes from a Beast TCP stream into the decoder.
// Returns number of messages decoded.
func (d *Decoder) Feed(data []byte) int {
	d.buf = append(d.buf, data...)
	decoded := 0

	for {
		// Find message start (0x1A)
		startIdx := -1
		for i := range d.buf {
			if d.buf[i] == 0x1A && i+1 < len(d.buf) && d.buf[i+1] != 0x1A {
				startIdx = i
				break
			}
		}
		if startIdx < 0 {
			break
		}

		// Skip to start
		d.buf = d.buf[startIdx:]
		if len(d.buf) < 2 {
			break
		}

		// Determine message type and expected length
		msgType := d.buf[1]
		var payloadLen int
		switch msgType {
		case '1': // Mode-AC
			payloadLen = 2
		case '2': // Mode-S short
			payloadLen = 7
		case '3': // Mode-S long (ADS-B)
			payloadLen = 14
		case '4': // Status
			payloadLen = 14
		default:
			// Unknown type, skip this byte
			d.buf = d.buf[1:]
			continue
		}

		// Total raw frame: 0x1A(1) + type(1) + timestamp(6) + signal(1) + payload
		minLen := 2 + 6 + 1 + payloadLen
		if len(d.buf) < minLen {
			break // Need more data
		}

		// Extract and un-escape the frame
		endIdx := minLen * 2
		if endIdx > len(d.buf) {
			endIdx = len(d.buf)
		}
		frame := d.unescape(d.buf[:endIdx])
		if frame == nil || len(frame) < 2+6+1+payloadLen {
			// Not enough data after unescaping, try to get more
			if len(d.buf) < minLen*2 {
				break
			}
			d.buf = d.buf[1:]
			d.errCount++
			continue
		}

		// Parse frame
		// timestamp := frame[2:8] // 6 bytes, 12MHz clock
		// signal := frame[8]
		payload := frame[9 : 9+payloadLen]

		// Advance buffer past this message
		consumed := d.findConsumed(minLen)
		d.buf = d.buf[consumed:]

		// Only decode Mode-S long (type 3) for ADS-B
		if msgType == '3' {
			d.decodeModeSLong(payload)
			decoded++
		}
		d.msgCount++
	}

	return decoded
}

// unescape removes Beast escape sequences (0x1A 0x1A → 0x1A)
func (d *Decoder) unescape(raw []byte) []byte {
	result := make([]byte, 0, len(raw))
	i := 0
	// Skip initial 0x1A marker
	if len(raw) > 0 && raw[0] == 0x1A {
		result = append(result, raw[0])
		i = 1
	}
	for i < len(raw) {
		if raw[i] == 0x1A {
			if i+1 < len(raw) && raw[i+1] == 0x1A {
				// Escaped 0x1A
				result = append(result, 0x1A)
				i += 2
			} else {
				// Next message start — stop here
				break
			}
		} else {
			result = append(result, raw[i])
			i++
		}
	}
	return result
}

// findConsumed finds how many raw bytes were consumed for one message
func (d *Decoder) findConsumed(minLen int) int {
	consumed := 0
	dataBytes := 0
	first := true
	for consumed < len(d.buf) && dataBytes < minLen {
		if d.buf[consumed] == 0x1A && !first {
			if consumed+1 < len(d.buf) && d.buf[consumed+1] == 0x1A {
				consumed += 2
				dataBytes++
				continue
			}
			// Next message start
			break
		}
		first = false
		consumed++
		dataBytes++
	}
	return consumed
}

// decodeModeSLong processes a 14-byte Mode-S long message (112 bits).
func (d *Decoder) decodeModeSLong(payload []byte) {
	if len(payload) < 14 {
		return
	}

	df := (payload[0] >> 3) & 0x1F
	if df != 17 && df != 18 {
		return // Only DF17 (ADS-B) and DF18 (TIS-B)
	}

	icao := uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
	tc := (payload[4] >> 3) & 0x1F

	d.mu.Lock()
	ac, exists := d.aircraft[icao]
	if !exists {
		ac = &Aircraft{ICAO: icao}
		d.aircraft[icao] = ac
	}
	ac.LastSeen = time.Now()
	d.mu.Unlock()

	switch {
	case tc >= 1 && tc <= 4:
		d.decodeIdentification(ac, payload[4:])
	case tc >= 9 && tc <= 18:
		d.decodeAirbornePosition(ac, payload[4:], false)
	case tc == 19:
		d.decodeAirborneVelocity(ac, payload[4:])
	case tc >= 20 && tc <= 22:
		d.decodeAirbornePosition(ac, payload[4:], true)
	}

	if d.onUpdate != nil {
		d.onUpdate(ac)
	}
}

// decodeIdentification decodes TC 1-4: Aircraft identification and category.
func (d *Decoder) decodeIdentification(ac *Aircraft, me []byte) {
	if len(me) < 7 {
		return
	}

	ac.Category = me[0] & 0x07

	// 6-bit characters encoded in 48 bits (8 chars)
	charset := "?ABCDEFGHIJKLMNOPQRSTUVWXYZ????? ???????????????0123456789??????"
	chars := make([]byte, 8)
	bits := uint64(me[1])<<40 | uint64(me[2])<<32 | uint64(me[3])<<24 |
		uint64(me[4])<<16 | uint64(me[5])<<8 | uint64(me[6])

	for i := 0; i < 8; i++ {
		idx := (bits >> (42 - uint(i)*6)) & 0x3F
		chars[i] = charset[idx]
	}

	d.mu.Lock()
	ac.Callsign = strings.TrimRight(string(chars), " ?")
	d.mu.Unlock()
}

// decodeAirbornePosition decodes TC 9-18/20-22: CPR airborne position.
func (d *Decoder) decodeAirbornePosition(ac *Aircraft, me []byte, gnssAlt bool) {
	if len(me) < 7 {
		return
	}

	// Altitude
	altBits := (uint16(me[1])<<4 | uint16(me[2])>>4) & 0xFFF
	qBit := (altBits >> 4) & 1

	var altitude int32
	if qBit == 1 {
		// Q=1: altitude = N*25 - 1000
		n := ((altBits & 0xFE0) >> 1) | (altBits & 0x0F)
		altitude = int32(n)*25 - 1000
	} else {
		// Gillham-encoded, simplified
		altitude = int32(altBits) * 100
	}

	// CPR latitude and longitude
	cprOddFlag := (me[2] >> 2) & 1
	latCPR := float64(uint32(me[2]&0x03)<<15|uint32(me[3])<<7|uint32(me[4])>>1) / 131072.0
	lonCPR := float64(uint32(me[4]&0x01)<<16|uint32(me[5])<<8|uint32(me[6])) / 131072.0

	d.mu.Lock()
	ac.Altitude = altitude

	if cprOddFlag == 0 {
		ac.EvenLat = latCPR
		ac.EvenLon = lonCPR
		ac.HasEvenCPR = true
		ac.EvenTime = time.Now()
	} else {
		ac.OddLat = latCPR
		ac.OddLon = lonCPR
		ac.HasOddCPR = true
		ac.OddTime = time.Now()
	}

	// Try to decode position if we have both even and odd
	if ac.HasEvenCPR && ac.HasOddCPR {
		lat, lon, ok := decodeCPRGlobal(ac.EvenLat, ac.EvenLon, ac.OddLat, ac.OddLon, cprOddFlag == 1)
		if ok {
			ac.Latitude = lat
			ac.Longitude = lon
			ac.PosValid = true
		}
	}
	d.mu.Unlock()
}

// decodeAirborneVelocity decodes TC 19: Airborne velocity.
func (d *Decoder) decodeAirborneVelocity(ac *Aircraft, me []byte) {
	if len(me) < 7 {
		return
	}

	subtype := me[0] & 0x07
	if subtype < 1 || subtype > 4 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if subtype == 1 || subtype == 2 {
		// Ground speed (subtype 1=subsonic, 2=supersonic)
		ewDir := (me[1] >> 2) & 1
		ewVel := int32(uint16(me[1]&0x03)<<8|uint16(me[2])) - 1
		nsDir := (me[3] >> 7) & 1
		nsVel := int32(uint16(me[3]&0x7F)<<3|uint16(me[4])>>5) - 1

		if subtype == 2 {
			ewVel *= 4
			nsVel *= 4
		}

		vEW := float64(ewVel)
		if ewDir == 1 {
			vEW = -vEW
		}
		vNS := float64(nsVel)
		if nsDir == 1 {
			vNS = -vNS
		}

		ac.Speed = math.Sqrt(vEW*vEW + vNS*vNS)
		ac.Heading = math.Atan2(vEW, vNS) * 180.0 / math.Pi
		if ac.Heading < 0 {
			ac.Heading += 360
		}
	} else {
		// Airspeed (subtype 3/4)
		headingAvail := (me[1] >> 2) & 1
		if headingAvail == 1 {
			heading := float64(uint16(me[1]&0x03)<<8|uint16(me[2])) * 360.0 / 1024.0
			ac.Heading = heading
		}
		as := int32(uint16(me[3]&0x7F)<<3|uint16(me[4])>>5) - 1
		ac.Speed = float64(as)
	}

	// Vertical rate
	vrSign := (me[4] >> 3) & 1
	vrRaw := int32(uint16(me[4]&0x07)<<6|uint16(me[5])>>2) - 1
	vr := vrRaw * 64
	if vrSign == 1 {
		vr = -vr
	}
	ac.VRate = vr
}

// decodeCPRGlobal performs global CPR decoding to get lat/lon.
func decodeCPRGlobal(evenLat, evenLon, oddLat, oddLon float64, mostRecentOdd bool) (lat, lon float64, ok bool) {
	const nzEven = 60.0
	const nzOdd = 59.0

	dLatEven := 360.0 / nzEven
	dLatOdd := 360.0 / nzOdd

	// Latitude index
	j := math.Floor(nzEven*oddLat - nzOdd*evenLat + 0.5)

	var latEven, latOdd float64
	latEven = dLatEven * (mod(j, nzEven) + evenLat)
	latOdd = dLatOdd * (mod(j, nzOdd) + oddLat)

	if latEven >= 270 {
		latEven -= 360
	}
	if latOdd >= 270 {
		latOdd -= 360
	}

	// Check zone consistency
	nlEven := cprNL(latEven)
	nlOdd := cprNL(latOdd)
	if nlEven != nlOdd {
		return 0, 0, false
	}

	// Use the most recent frame
	if mostRecentOdd {
		lat = latOdd
		nl := cprNL(lat)
		if nl-1 <= 0 {
			lon = 360.0 * oddLon
		} else {
			dLon := 360.0 / float64(nl-1)
			m := math.Floor(evenLon*float64(nl-1) - oddLon*float64(nl) + 0.5)
			lon = dLon * (mod(m, float64(nl-1)) + oddLon)
		}
	} else {
		lat = latEven
		nl := cprNL(lat)
		if nl <= 0 {
			lon = 360.0 * evenLon
		} else {
			dLon := 360.0 / float64(nl)
			m := math.Floor(evenLon*float64(nl) - oddLon*float64(nl-1) + 0.5)
			lon = dLon * (mod(m, float64(nl)) + evenLon)
		}
	}

	if lon > 180 {
		lon -= 360
	}

	// Sanity check
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}

	return lat, lon, true
}

// cprNL returns the number of longitude zones for a given latitude.
func cprNL(lat float64) int {
	if math.Abs(lat) >= 87.0 {
		return 1
	}
	nz := 15.0
	a := 1 - math.Cos(math.Pi/(2*nz))
	b := math.Cos(math.Pi / 180.0 * math.Abs(lat))
	nl := math.Floor(2.0 * math.Pi / math.Acos(1-a/(b*b)))
	return int(nl)
}

func mod(a, b float64) float64 {
	r := math.Mod(a, b)
	if r < 0 {
		r += b
	}
	return r
}

// GetAircraft returns the current aircraft map (read-only snapshot).
func (d *Decoder) GetAircraft() map[uint32]*Aircraft {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make(map[uint32]*Aircraft, len(d.aircraft))
	for k, v := range d.aircraft {
		cp := *v
		result[k] = &cp
	}
	return result
}

// GetActiveCount returns number of aircraft seen in the last N seconds.
func (d *Decoder) GetActiveCount(maxAge time.Duration) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	cutoff := time.Now().Add(-maxAge)
	count := 0
	for _, ac := range d.aircraft {
		if ac.LastSeen.After(cutoff) {
			count++
		}
	}
	return count
}

// Stats returns decoder statistics.
func (d *Decoder) Stats() (messages, errors uint64) {
	return d.msgCount, d.errCount
}

// ToEntityState converts an Aircraft to the canonical EntityState.
func (ac *Aircraft) ToEntityState(seq uint32) generator.EntityState {
	flags := generator.FlagActive
	if ac.PosValid {
		flags |= generator.FlagPositionValid
	}
	if ac.Altitude != 0 {
		flags |= generator.FlagAltitudeValid
	}
	if ac.Speed > 0 {
		flags |= generator.FlagSpeedValid
	}
	if ac.Heading > 0 {
		flags |= generator.FlagHeadingValid
	}
	if ac.VRate != 0 {
		flags |= generator.FlagVRateValid
	}

	return generator.EntityState{
		EntityID:    ac.ICAO,
		Flags:       flags,
		EntityType:  generator.TypeAircraft,
		TimestampMs: uint64(ac.LastSeen.UnixMilli()),
		Latitude:    ac.Latitude,
		Longitude:   ac.Longitude,
		AltitudeM:   float32(ac.Altitude) * 0.3048, // feet → meters
		SpeedMs:     float32(ac.Speed) * 0.514444,   // knots → m/s
		HeadingDeg:  float32(ac.Heading),
		VRateMs:     float32(ac.VRate) * 0.00508,    // ft/min → m/s
		Sequence:    seq,
		GridCell:    generator.ComputeGridCell(ac.Latitude, ac.Longitude, 1.0),
	}
}

// String returns a summary of the aircraft.
func (ac *Aircraft) String() string {
	return fmt.Sprintf("ICAO=%06X call=%s lat=%.4f lon=%.4f alt=%dft spd=%.0fkt hdg=%.0f°",
		ac.ICAO, ac.Callsign, ac.Latitude, ac.Longitude, ac.Altitude, ac.Speed, ac.Heading)
}
