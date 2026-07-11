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
	LastPosTime time.Time
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
	MsgCount   uint32
	PosCount   uint32
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

	// CRC-24 validation — reject corrupt messages
	valid, _ := ValidateCRC(payload)
	if !valid {
		d.errCount++
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

	// Altitude (12-bit field with Q-bit)
	altCode := (uint16(me[1])<<4 | uint16(me[2])>>4) & 0xFFF
	qBit := (altCode >> 4) & 1

	var altitude int32
	if altCode == 0 {
		altitude = 0 // not available
	} else if qBit == 1 {
		// Q=1: 25ft resolution
		n := int32(((altCode>>5)&0x7F)<<4 | (altCode & 0x0F))
		altitude = n*25 - 1000
	} else {
		// Q=0: Gillham code (100ft resolution), simplified
		altitude = int32(altCode&0x7FF) * 100
	}

	// Altitude sanity check
	if !validateAltitude(altitude) {
		return
	}

	// CPR latitude and longitude
	cprOddFlag := (me[2] >> 2) & 1
	latCPR := float64(uint32(me[2]&0x03)<<15|uint32(me[3])<<7|uint32(me[4])>>1) / 131072.0
	lonCPR := float64(uint32(me[4]&0x01)<<16|uint32(me[5])<<8|uint32(me[6])) / 131072.0

	now := time.Now()
	isOdd := cprOddFlag == 1

	d.mu.Lock()
	defer d.mu.Unlock()

	ac.Altitude = altitude
	ac.MsgCount++

	// Store CPR frame
	if isOdd {
		ac.OddLat = latCPR
		ac.OddLon = lonCPR
		ac.HasOddCPR = true
		ac.OddTime = now
	} else {
		ac.EvenLat = latCPR
		ac.EvenLon = lonCPR
		ac.HasEvenCPR = true
		ac.EvenTime = now
	}

	// Attempt position decode
	var lat, lon float64
	var ok bool

	if ac.PosValid {
		// Local CPR decode (preferred after initial fix)
		lat, lon, ok = decodeCPRLocal(ac.Latitude, ac.Longitude, latCPR, lonCPR, isOdd)
		if ok {
			// Sanity check against previous position
			if !validatePosition(ac, lat, lon, now) {
				ok = false
			}
		}
	}

	if !ok && ac.HasEvenCPR && ac.HasOddCPR {
		// Fall back to global CPR decode
		timeDiff := ac.EvenTime.Sub(ac.OddTime)
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}
		if timeDiff < cprGlobalMaxAge {
			lat, lon, ok = decodeCPRGlobal(ac.EvenLat, ac.EvenLon, ac.OddLat, ac.OddLon, isOdd)
			if ok && ac.PosValid {
				// Even global decode must pass sanity check after first fix
				if !validatePosition(ac, lat, lon, now) {
					ok = false
				}
			}
		}
	}

	if ok {
		ac.Latitude = lat
		ac.Longitude = lon
		ac.PosValid = true
		ac.LastPosTime = now
		ac.PosCount++
	}
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
// Requires both even and odd CPR frames to compute absolute position.
func decodeCPRGlobal(evenLat, evenLon, oddLat, oddLon float64, mostRecentOdd bool) (lat, lon float64, ok bool) {
	const airDLatEven = 360.0 / 60.0 // 6.0 degrees
	const airDLatOdd = 360.0 / 59.0  // ~6.1 degrees

	// Compute latitude index j
	j := math.Floor(59.0*evenLat - 60.0*oddLat + 0.5)

	// Compute latitudes for even and odd
	latEven := airDLatEven * (mod(j, 60.0) + evenLat)
	latOdd := airDLatOdd * (mod(j, 59.0) + oddLat)

	// Normalize to [-90, 90]
	if latEven >= 270.0 {
		latEven -= 360.0
	}
	if latOdd >= 270.0 {
		latOdd -= 360.0
	}

	// Check NL zone consistency - if they differ, the frames crossed a zone boundary
	nlEven := cprNL(latEven)
	nlOdd := cprNL(latOdd)
	if nlEven != nlOdd {
		return 0, 0, false
	}

	// Compute longitude
	if mostRecentOdd {
		lat = latOdd
		nl := float64(cprNL(lat))
		ni := math.Max(nl-1, 1)
		m := math.Floor(evenLon*(nl-1) - oddLon*nl + 0.5)
		lon = (360.0 / ni) * (mod(m, ni) + oddLon)
	} else {
		lat = latEven
		nl := float64(cprNL(lat))
		ni := math.Max(nl, 1)
		m := math.Floor(evenLon*(nl-1) - oddLon*nl + 0.5)
		lon = (360.0 / ni) * (mod(m, ni) + evenLon)
	}

	// Normalize longitude to [-180, 180]
	if lon >= 180.0 {
		lon -= 360.0
	}

	// Final sanity check
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}

	return lat, lon, true
}

// cprNL returns the number of longitude zones (NL) for a given latitude.
// This is the key lookup table for CPR decoding per ICAO Annex 10.
func cprNL(lat float64) int {
	lat = math.Abs(lat)
	switch {
	case lat < 10.47047130:
		return 59
	case lat < 14.82817437:
		return 58
	case lat < 18.18626357:
		return 57
	case lat < 21.02939493:
		return 56
	case lat < 23.54504487:
		return 55
	case lat < 25.82924707:
		return 54
	case lat < 27.93898710:
		return 53
	case lat < 29.91135686:
		return 52
	case lat < 31.77209708:
		return 51
	case lat < 33.53993436:
		return 50
	case lat < 35.22899598:
		return 49
	case lat < 36.85025108:
		return 48
	case lat < 38.41241892:
		return 47
	case lat < 39.92256684:
		return 46
	case lat < 41.38651832:
		return 45
	case lat < 42.80914012:
		return 44
	case lat < 44.19454951:
		return 43
	case lat < 45.54626723:
		return 42
	case lat < 46.86733252:
		return 41
	case lat < 48.16039128:
		return 40
	case lat < 49.42776439:
		return 39
	case lat < 50.67150166:
		return 38
	case lat < 51.89342469:
		return 37
	case lat < 53.09516153:
		return 36
	case lat < 54.27817472:
		return 35
	case lat < 55.44378444:
		return 34
	case lat < 56.59318756:
		return 33
	case lat < 57.72747354:
		return 32
	case lat < 58.84763776:
		return 31
	case lat < 59.95459277:
		return 30
	case lat < 61.04917774:
		return 29
	case lat < 62.13216659:
		return 28
	case lat < 63.20427479:
		return 27
	case lat < 64.26616523:
		return 26
	case lat < 65.31845310:
		return 25
	case lat < 66.36171008:
		return 24
	case lat < 67.39646774:
		return 23
	case lat < 68.42322022:
		return 22
	case lat < 69.44242631:
		return 21
	case lat < 70.45451075:
		return 20
	case lat < 71.45986473:
		return 19
	case lat < 72.45884545:
		return 18
	case lat < 73.45177442:
		return 17
	case lat < 74.43893416:
		return 16
	case lat < 75.42056257:
		return 15
	case lat < 76.39684391:
		return 14
	case lat < 77.36789461:
		return 13
	case lat < 78.33374083:
		return 12
	case lat < 79.29428225:
		return 11
	case lat < 80.24923213:
		return 10
	case lat < 81.19801349:
		return 9
	case lat < 82.13956981:
		return 8
	case lat < 83.07199445:
		return 7
	case lat < 83.99173563:
		return 6
	case lat < 84.89166191:
		return 5
	case lat < 85.75541621:
		return 4
	case lat < 86.53536998:
		return 3
	case lat < 87.00000000:
		return 2
	default:
		return 1
	}
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
	altMeters := float32(ac.Altitude) * 0.3048
	if ac.Altitude != 0 && ac.Altitude > 0 && ac.Altitude < 60000 {
		flags |= generator.FlagAltitudeValid
	} else {
		altMeters = 0
	}
	if ac.Speed > 0 && ac.Speed < 900 {
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
		AltitudeM:   altMeters,
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
