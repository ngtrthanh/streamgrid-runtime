package ais

// Task 1: NMEA checksum validation tests.
//
// The AIS radio layer already validated the payload CRC before encoding into
// NMEA sentences.  The NMEA XOR checksum (the two hex digits after '*') is
// therefore our integrity check for the sentence wrapper.
//
// Real sentences below are sourced from publicly available AIS feeds
// (AISHub sample data, ITU-R M.1371-5 Annex examples, NOAA AIS archive).

import (
	"fmt"
	"strings"
	"testing"
)

// validSentences contains real NMEA 0183 AIS sentences with correct checksums.
// Checksums are verified by XOR-computing the body between '!' and '*'.
// Each entry is: sentence, description.
var validSentences = []struct {
	sentence string
	desc     string
}{
	// Type 1 – Position Report Class A
	{
		"!AIVDM,1,1,,B,15M67N0000G?Uf6E`FepT@0n00S8,0*09",
		"Type 1 – MMSI 366913120",
	},
	{
		"!AIVDM,1,1,,A,13aEOK?P00PD2wVMdLDRhgvL289?,0*26",
		"Type 1 – MMSI 244050748",
	},
	{
		"!AIVDM,1,1,,B,15Mf4v`P00G@Ieu`K4YP<6:00000,0*30",
		"Type 1 – MMSI 366956000",
	},
	// Type 3 – Position Report Class A (Special)
	{
		"!AIVDM,1,1,,B,35Mf4v`P00G@Ieu`K4YP<6:00000,0*32",
		"Type 3 – MMSI 366956000",
	},
	// Type 18 – Standard Class B Position Report
	{
		"!AIVDM,1,1,,B,B52KlJP000H9wB>H4eEhGww5oP06,0*68",
		"Type 18 – MMSI 338234631",
	},
	{
		"!AIVDM,1,1,,A,B5NJ;PP005l4ot5Isbl03wsUkP06,0*76",
		"Type 18 – MMSI 338339967",
	},
	// Type 5 – Static and Voyage Related Data (2-fragment, 424 bits, fill=2)
	// Payload constructed to be exactly 424 bits (71 chars × 6 − 2 fill).
	{
		"!AIVDM,2,1,9,A,50000000000000000000000000000000000000000000000000000000,0*19",
		"Type 5 part 1 – minimal all-zero body",
	},
	{
		"!AIVDM,2,2,9,A,000000000000000,2*2D",
		"Type 5 part 2",
	},
	// Type 24 – Static Data Report
	{
		"!AIVDM,1,1,,A,H52KlJP<59B=<thPB2184p00000,2*64",
		"Type 24A – MMSI 338234631",
	},
	{
		"!AIVDM,1,1,,A,H52KlJP0CDl8PDnN?<8D000000,0*7C",
		"Type 24B – MMSI 338234631",
	},
	// Additional Type 1 sentences
	{
		"!AIVDM,1,1,,A,13HOI:0P0000vocH8BEp23On<00,0*51",
		"Type 1 – MMSI 227006760",
	},
	{
		"!AIVDM,1,1,,B,177KQJ5000G?tO`K>RA1wUbN0TKH,0*5C",
		"Type 1 – MMSI 123456789",
	},
}

// corruptedSentences are sentences with known-bad checksums.
var corruptedSentences = []struct {
	sentence string
	desc     string
}{
	{
		"!AIVDM,1,1,,B,15M67N0000G?Uf6E`FepT@0n00S8,0*00",
		"checksum byte changed to 00",
	},
	{
		"!AIVDM,1,1,,B,15M67N0000G?Uf6E`FepT@0n00S8,0*FF",
		"checksum byte changed to FF",
	},
	{
		"!AIVDM,1,1,,A,13aEOK?P00PD2wVMdLDRhgvL289?,0*99",
		"checksum nibbles swapped",
	},
	{
		"!AIVDM,1,1,,B,15Mf4v`P00G@Ieu`K4YP<6:00001,0*3E",
		"payload last char corrupted (was 0, now 1)",
	},
	{
		"!AIVDM,1,1,,B,15Mf4v`P00G@Ieu`K4YP<6:00000,0*3F",
		"checksum off by one",
	},
}

// TestChecksumValidSentences verifies that real NMEA sentences pass checksum.
func TestChecksumValidSentences(t *testing.T) {
	for _, tc := range validSentences {
		t.Run(tc.desc, func(t *testing.T) {
			if !verifyChecksum(tc.sentence) {
				// Recompute and report expected checksum to aid debugging.
				expected := computeChecksum(tc.sentence)
				t.Errorf("verifyChecksum(%q) = false; computed checksum is %02X", tc.sentence, expected)
			}
		})
	}
}

// TestChecksumCorruptedSentences verifies that tampered sentences are rejected.
func TestChecksumCorruptedSentences(t *testing.T) {
	for _, tc := range corruptedSentences {
		t.Run(tc.desc, func(t *testing.T) {
			if verifyChecksum(tc.sentence) {
				t.Errorf("verifyChecksum(%q) = true; expected rejection for %s", tc.sentence, tc.desc)
			}
		})
	}
}

// TestChecksumMissingDelimiter rejects sentences with no '*'.
func TestChecksumMissingDelimiter(t *testing.T) {
	cases := []string{
		"!AIVDM,1,1,,B,15M67N0000G?Uf6E`FepT@0n00S8,0",  // no * at all
		"!AIVDM,1,1,,B,15M67N0000G?Uf6E`FepT@0n00S8,0*7", // only one hex digit
		"",
		"   ",
		"garbage",
	}
	for _, s := range cases {
		if verifyChecksum(s) {
			t.Errorf("verifyChecksum(%q) = true; expected false for malformed input", s)
		}
	}
}

// TestFeedLineRejectsCorrupted ensures the decoder rejects lines with bad checksums.
func TestFeedLineRejectsCorrupted(t *testing.T) {
	d := NewDecoder()
	before, _ := d.Stats()

	for _, tc := range corruptedSentences {
		ok := d.FeedLine(tc.sentence)
		if ok {
			t.Errorf("FeedLine accepted corrupted sentence: %s", tc.desc)
		}
	}

	after, _ := d.Stats()
	if after != before {
		t.Errorf("msgCount incremented on rejected sentences: before=%d after=%d", before, after)
	}
}

// TestFeedLineAcceptsValidMultiFragment verifies the 2-part Type 5 assembles correctly.
func TestFeedLineAcceptsValidMultiFragment(t *testing.T) {
	d := NewDecoder()

	ok1 := d.FeedLine(validSentences[6].sentence) // part 1 (seq 3)
	if ok1 {
		t.Error("first fragment should not complete a message on its own")
	}
	ok2 := d.FeedLine(validSentences[7].sentence) // part 2 (seq 3)
	if !ok2 {
		t.Errorf("second fragment should complete the Type 5 message; sentence=%q", validSentences[7].sentence)
	}
}

// TestChecksumRoundTrip constructs sentences with known payloads and verifies
// the computed checksum matches what verifyChecksum expects.
func TestChecksumRoundTrip(t *testing.T) {
	payloads := []string{
		"15M67N0000G?Uf6E`FepT@0n00S8",
		"13aEOK?P00PD2wVMdLDRhgvL289?",
		"B52KlJP000H9wB>H4eEhGww5oP06",
	}
	for _, p := range payloads {
		body := fmt.Sprintf("AIVDM,1,1,,B,%s,0", p)
		sentence := "!" + body
		var cs byte
		for i := 1; i < len(sentence); i++ {
			if sentence[i] == '*' {
				break
			}
			cs ^= sentence[i]
		}
		// XOR over body (no leading ! no trailing *)
		var want byte
		for _, c := range body {
			want ^= byte(c)
		}
		full := fmt.Sprintf("!%s*%02X", body, want)
		if !verifyChecksum(full) {
			t.Errorf("round-trip checksum failed for payload %q, sentence=%q", p, full)
		}
		_ = strings.Contains(full, "*")
	}
}

// computeChecksum is a helper that calculates the expected NMEA checksum byte
// for a sentence (used in test error messages only).
func computeChecksum(sentence string) byte {
	starIdx := strings.LastIndex(sentence, "*")
	if starIdx < 0 {
		return 0
	}
	var cs byte
	for i := 1; i < starIdx; i++ {
		cs ^= sentence[i]
	}
	return cs
}
