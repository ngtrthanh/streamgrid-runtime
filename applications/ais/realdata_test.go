//go:build realdata

package ais

import (
	"bufio"
	"fmt"
	"os"
	"testing"
)

// TestRealNMEA loads the captured NMEA fixture and feeds all lines through
// the AIS decoder, reporting totals for lines, decoded messages, unique
// vessels, and vessels with a valid position.
func TestRealNMEA(t *testing.T) {
	const fixture = "testdata/real_nmea.txt"

	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open %s: %v (run: timeout 5 nc localhost 5015 > %s)", fixture, err, fixture)
	}
	defer f.Close()

	dec := NewDecoder()
	var totalLines, decodedMsgs int

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		totalLines++
		if dec.FeedLine(scanner.Text()) {
			decodedMsgs++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan error: %v", err)
	}

	vessels := dec.GetVessels()
	uniqueVessels := len(vessels)

	withPos := 0
	for _, v := range vessels {
		if v.PosValid {
			withPos++
		}
	}

	fmt.Printf("\n=== AIS Real Data Report ===\n")
	fmt.Printf("  Total lines:        %d\n", totalLines)
	fmt.Printf("  Decoded messages:   %d\n", decodedMsgs)
	fmt.Printf("  Unique vessels:     %d\n", uniqueVessels)
	fmt.Printf("  Vessels with pos:   %d\n", withPos)
	fmt.Printf("============================\n\n")

	// Sanity checks — we expect some data if the feed was live
	if totalLines == 0 {
		t.Error("no lines in fixture — re-capture with: timeout 5 nc localhost 5015 > testdata/real_nmea.txt")
	}
	if decodedMsgs == 0 && totalLines > 0 {
		t.Error("no messages decoded despite having lines — check NMEA format")
	}
	if uniqueVessels == 0 && decodedMsgs > 0 {
		t.Error("messages decoded but no vessels tracked — check MMSI extraction")
	}
}
