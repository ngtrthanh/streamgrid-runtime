package adsb

import (
	"math"
	"time"
)

// CPR decoding constants
const (
	// Maximum time between even/odd frames for global decode
	cprGlobalMaxAge = 10 * time.Second
	// Maximum distance for local decode reference (180 NM in degrees approx)
	cprLocalMaxDistance = 3.0 // degrees (~180 NM at equator)
	// Maximum position jump between consecutive decodes
	maxPositionJumpDeg = 0.83 // ~50 NM
	// Maximum speed for sanity check (900 knots)
	maxSpeedKnots = 900.0
	// Maximum altitude for airborne (60000 ft)
	maxAltitudeFt = 60000
	// Maximum vertical rate (6000 ft/min)
	maxVRateFtMin = 10000
	// Stale aircraft timeout
	staleTimeout = 60 * time.Second
)

// decodeCPRLocal performs local CPR decode using a reference position.
// This is faster and more robust than global decode for subsequent position updates.
func decodeCPRLocal(refLat, refLon float64, cprLat, cprLon float64, isOdd bool) (lat, lon float64, ok bool) {
	var dLat float64
	if isOdd {
		dLat = 360.0 / 59.0
	} else {
		dLat = 360.0 / 60.0
	}

	// Latitude
	j := math.Floor(refLat/dLat) + math.Floor(0.5+(math.Mod(refLat, dLat)/dLat)-cprLat)
	lat = dLat * (j + cprLat)

	if lat < -90 || lat > 90 {
		return 0, 0, false
	}

	// Longitude
	nl := float64(cprNL(lat))
	var ni float64
	if isOdd {
		ni = math.Max(nl-1, 1)
	} else {
		ni = math.Max(nl, 1)
	}
	dLon := 360.0 / ni

	m := math.Floor(refLon/dLon) + math.Floor(0.5+(math.Mod(refLon, dLon)/dLon)-cprLon)
	lon = dLon * (m + cprLon)

	if lon > 180 {
		lon -= 360
	}
	if lon < -180 {
		lon += 360
	}

	return lat, lon, true
}

// validatePosition checks if a new position is reasonable given the previous state.
func validatePosition(ac *Aircraft, newLat, newLon float64, now time.Time) bool {
	// First position — always accept (after CRC validation)
	if !ac.PosValid {
		return true
	}

	// Time since last position
	dt := now.Sub(ac.LastPosTime).Seconds()
	if dt <= 0 {
		dt = 1.0
	}

	// Distance check
	dLat := newLat - ac.Latitude
	dLon := newLon - ac.Longitude
	dist := math.Sqrt(dLat*dLat + dLon*dLon)

	// Maximum allowed distance based on time elapsed
	// At 900 knots (max), aircraft covers ~0.25 degrees per second
	maxDist := maxPositionJumpDeg
	if dt < 60 {
		// Allow proportional to time, with minimum threshold
		maxDist = math.Max(0.01, 0.25*dt) // ~0.25 deg/s at max speed
	}

	if dist > maxDist {
		return false
	}

	return true
}

// validateAltitude checks if altitude is reasonable.
func validateAltitude(alt int32) bool {
	return alt > -2000 && alt < maxAltitudeFt
}

// validateSpeed checks if speed is reasonable.
func validateSpeed(speed float64) bool {
	return speed >= 0 && speed < maxSpeedKnots
}

// PruneStale removes aircraft not seen within the stale timeout.
// Returns number of aircraft removed.
func (d *Decoder) PruneStale() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	cutoff := time.Now().Add(-staleTimeout)
	pruned := 0
	for icao, ac := range d.aircraft {
		if ac.LastSeen.Before(cutoff) {
			delete(d.aircraft, icao)
			pruned++
		}
	}
	return pruned
}

// ActiveCount returns number of aircraft seen within the given duration.
func (d *Decoder) ActiveCount(maxAge time.Duration) int {
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

// GetActiveAircraft returns only aircraft seen within maxAge with valid positions.
func (d *Decoder) GetActiveAircraft(maxAge time.Duration) []*Aircraft {
	d.mu.RLock()
	defer d.mu.RUnlock()
	cutoff := time.Now().Add(-maxAge)
	result := make([]*Aircraft, 0, 256)
	for _, ac := range d.aircraft {
		if ac.LastSeen.After(cutoff) && ac.PosValid {
			cp := *ac
			result = append(result, &cp)
		}
	}
	return result
}
