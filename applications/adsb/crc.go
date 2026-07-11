package adsb

// Mode-S CRC-24 computation.
//
// The generator polynomial is:
//   G(x) = x^24 + x^23 + x^22 + x^21 + x^20 + x^19 + x^18 + x^17 +
//           x^16 + x^15 + x^14 + x^13 + x^10 + x^3 + 1
//   = 0x1FFF409 (25 bits with leading 1)
//
// For DF17 (ADS-B), the last 3 bytes of the 14-byte message are the PI
// (Parity/Interrogator) field. Computing CRC over the first 11 bytes
// should equal the last 3 bytes (for DF17, PI = CRC).
//
// For DF11/DF17/DF18 downlink formats, valid messages have remainder 0
// when CRC is computed over all 14 bytes (or the PI equals the computed CRC
// of the first 11 bytes).

const crc24Poly = 0xFFF409

// crc24Table is a precomputed lookup table for fast CRC-24 computation.
var crc24Table [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		crc := uint32(i) << 16
		for j := 0; j < 8; j++ {
			if crc&0x800000 != 0 {
				crc = (crc << 1) ^ crc24Poly
			} else {
				crc = crc << 1
			}
		}
		crc24Table[i] = crc & 0xFFFFFF
	}
}

// ComputeCRC24 computes the Mode-S CRC-24 over the given data.
func ComputeCRC24(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ crc24Table[((crc>>16)^uint32(b))&0xFF]
		crc &= 0xFFFFFF
	}
	return crc
}

// ValidateCRC checks a Mode-S long (14-byte) or short (7-byte) message.
// For DF17/18, the CRC of the first N-3 bytes should equal the last 3 bytes.
// Returns true if valid, and the extracted PI/AP field.
func ValidateCRC(msg []byte) (valid bool, remainder uint32) {
	if len(msg) < 7 {
		return false, 0
	}

	// Compute CRC over all bytes — if valid, remainder is 0 for DF17/18
	// For other DFs, remainder = ICAO XOR'd with AP
	msgLen := len(msg)
	dataLen := msgLen - 3

	computed := ComputeCRC24(msg[:dataLen])
	piField := uint32(msg[dataLen])<<16 | uint32(msg[dataLen+1])<<8 | uint32(msg[dataLen+2])

	df := (msg[0] >> 3) & 0x1F

	switch df {
	case 11, 17, 18:
		// For these DFs, PI field IS the CRC — check for zero remainder
		remainder = computed ^ piField
		valid = (remainder == 0)
	default:
		// For other DFs, AP field = ICAO XOR CRC
		// We can't validate without knowing the ICAO, but we return the remainder
		remainder = computed ^ piField
		valid = false // Can't validate without ICAO lookup
	}

	return valid, remainder
}
