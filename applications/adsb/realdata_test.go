//go:build realdata

package adsb

import (
	"fmt"
	"os"
	"testing"
)

// TestRealBeast loads the captured Beast binary fixture and feeds it through
// the ADS-B decoder, reporting totals for messages parsed, CRC-valid messages,
// unique ICAOs, and aircraft with a valid position.
func TestRealBeast(t *testing.T) {
	const fixture = "testdata/real_beast.bin"

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read %s: %v (run: timeout 5 nc 10.99.0.1 33005 > %s)", fixture, err, fixture)
	}

	dec := NewDecoder()

	// Track CRC-valid count by hooking into updates.
	// The decoder only counts a message in msgCount if it passes CRC, so we
	// compare before/after. We feed in one large chunk.
	decoded := dec.Feed(data)

	msgs, errs := dec.Stats()

	aircraft := dec.GetAircraft()
	uniqueICAOs := len(aircraft)

	withPos := 0
	for _, ac := range aircraft {
		if ac.PosValid {
			withPos++
		}
	}

	// CRC-valid = total decoded (Feed return value counts only processed frames,
	// but errCount captures CRC failures). crcValid = msgs - errs gives valid ones.
	crcValid := int(msgs) - int(errs)

	fmt.Printf("\n=== ADS-B Real Data Report ===\n")
	fmt.Printf("  Raw data bytes:     %d\n", len(data))
	fmt.Printf("  Messages fed:       %d\n", decoded)
	fmt.Printf("  Total msg count:    %d\n", msgs)
	fmt.Printf("  CRC valid:          %d\n", crcValid)
	fmt.Printf("  Unique ICAOs:       %d\n", uniqueICAOs)
	fmt.Printf("  Aircraft with pos:  %d\n", withPos)
	fmt.Printf("==============================\n\n")

	if len(data) == 0 {
		t.Error("empty fixture — re-capture with: timeout 5 nc 10.99.0.1 33005 > testdata/real_beast.bin")
	}
	if msgs == 0 && len(data) > 100 {
		t.Error("no messages decoded despite data being present — check Beast format parsing")
	}
	if uniqueICAOs == 0 && msgs > 0 {
		t.Error("messages decoded but no ICAOs tracked — check ICAO extraction")
	}
}
