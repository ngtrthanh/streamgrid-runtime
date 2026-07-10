// Package generator implements a synthetic telemetry generator for StreamGrid.
//
// It produces configurable numbers of moving entities with realistic trajectories
// (great-circle routes, random walks, holding patterns) for benchmarking and development.
package generator

import (
	"encoding/binary"
	"math"
	"math/rand"
	"sync/atomic"
	"time"
)

const (
	// EntityStateSize is the canonical entity record size in bytes.
	EntityStateSize = 64

	// FrameHeaderSize is the frame header size in bytes.
	FrameHeaderSize = 16

	// FrameMagic identifies StreamGrid frames.
	FrameMagic uint32 = 0x53475246 // "SGRF"

	// ProtocolVersion is the current protocol version.
	ProtocolVersion uint8 = 1
)

// Entity types
const (
	TypeUnknown  uint8 = 0x00
	TypeAircraft uint8 = 0x01
	TypeVessel   uint8 = 0x02
	TypeVehicle  uint8 = 0x03
	TypeDrone    uint8 = 0x05
	TypeSensor   uint8 = 0x07
)

// Flags
const (
	FlagActive        uint16 = 1 << 0
	FlagPositionValid uint16 = 1 << 1
	FlagAltitudeValid uint16 = 1 << 2
	FlagSpeedValid    uint16 = 1 << 3
	FlagHeadingValid  uint16 = 1 << 4
	FlagVRateValid    uint16 = 1 << 5
)

// EntityState is the Go representation of the 64-byte canonical entity.
type EntityState struct {
	EntityID    uint32  `json:"entity_id"`
	Flags       uint16  `json:"flags"`
	EntityType  uint8   `json:"entity_type"`
	Padding     uint8
	TimestampMs uint64  `json:"timestamp_ms"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	AltitudeM   float32 `json:"altitude_m"`
	SpeedMs     float32 `json:"speed_ms"`
	HeadingDeg  float32 `json:"heading_deg"`
	VRateMs     float32 `json:"vrate_ms"`
	Sequence    uint32  `json:"sequence"`
	GridCell    uint32  `json:"grid_cell"`
	Reserved    [8]byte
}

// FrameHeader is the 16-byte frame header.
type FrameHeader struct {
	Magic       uint32
	Version     uint8
	FrameType   uint8
	EntityCount uint16
	TimestampMs uint64
}

// MarshalBinary encodes an EntityState to 64 bytes (little-endian).
func (e *EntityState) MarshalBinary(buf []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], e.EntityID)
	binary.LittleEndian.PutUint16(buf[4:6], e.Flags)
	buf[6] = e.EntityType
	buf[7] = e.Padding
	binary.LittleEndian.PutUint64(buf[8:16], e.TimestampMs)
	binary.LittleEndian.PutUint64(buf[16:24], math.Float64bits(e.Latitude))
	binary.LittleEndian.PutUint64(buf[24:32], math.Float64bits(e.Longitude))
	binary.LittleEndian.PutUint32(buf[32:36], math.Float32bits(e.AltitudeM))
	binary.LittleEndian.PutUint32(buf[36:40], math.Float32bits(e.SpeedMs))
	binary.LittleEndian.PutUint32(buf[40:44], math.Float32bits(e.HeadingDeg))
	binary.LittleEndian.PutUint32(buf[44:48], math.Float32bits(e.VRateMs))
	binary.LittleEndian.PutUint32(buf[48:52], e.Sequence)
	binary.LittleEndian.PutUint32(buf[52:56], e.GridCell)
	copy(buf[56:64], e.Reserved[:])
}

// UnmarshalBinary decodes an EntityState from 64 bytes (little-endian).
func (e *EntityState) UnmarshalBinary(buf []byte) {
	e.EntityID = binary.LittleEndian.Uint32(buf[0:4])
	e.Flags = binary.LittleEndian.Uint16(buf[4:6])
	e.EntityType = buf[6]
	e.Padding = buf[7]
	e.TimestampMs = binary.LittleEndian.Uint64(buf[8:16])
	e.Latitude = math.Float64frombits(binary.LittleEndian.Uint64(buf[16:24]))
	e.Longitude = math.Float64frombits(binary.LittleEndian.Uint64(buf[24:32]))
	e.AltitudeM = math.Float32frombits(binary.LittleEndian.Uint32(buf[32:36]))
	e.SpeedMs = math.Float32frombits(binary.LittleEndian.Uint32(buf[36:40]))
	e.HeadingDeg = math.Float32frombits(binary.LittleEndian.Uint32(buf[40:44]))
	e.VRateMs = math.Float32frombits(binary.LittleEndian.Uint32(buf[44:48]))
	e.Sequence = binary.LittleEndian.Uint32(buf[48:52])
	e.GridCell = binary.LittleEndian.Uint32(buf[52:56])
	copy(e.Reserved[:], buf[56:64])
}

// MarshalFrameHeader encodes a frame header to 16 bytes.
func MarshalFrameHeader(h *FrameHeader, buf []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], h.Magic)
	buf[4] = h.Version
	buf[5] = h.FrameType
	binary.LittleEndian.PutUint16(buf[6:8], h.EntityCount)
	binary.LittleEndian.PutUint64(buf[8:16], h.TimestampMs)
}

// ComputeGridCell calculates the spatial grid cell for a lat/lon.
func ComputeGridCell(latitude, longitude, cellSizeDeg float64) uint32 {
	cellX := uint32(math.Floor((longitude + 180.0) / cellSizeDeg))
	cellY := uint32(math.Floor((latitude + 90.0) / cellSizeDeg))
	gridWidth := uint32(math.Ceil(360.0 / cellSizeDeg))
	return cellY*gridWidth + cellX
}

// MovementPattern defines how an entity moves.
type MovementPattern int

const (
	// PatternGreatCircle moves along a great-circle route between waypoints.
	PatternGreatCircle MovementPattern = iota
	// PatternRandomWalk applies random heading changes.
	PatternRandomWalk
	// PatternOrbit circles around a fixed point.
	PatternOrbit
	// PatternStationary stays in place (sensors, fixed assets).
	PatternStationary
)

// Config configures the telemetry generator.
type Config struct {
	EntityCount  int
	UpdateRateHz float64
	Seed         int64
	BoundsMinLat float64
	BoundsMaxLat float64
	BoundsMinLon float64
	BoundsMaxLon float64
	CellSizeDeg  float64
}

// DefaultConfig returns a reasonable default configuration.
func DefaultConfig() Config {
	return Config{
		EntityCount:  1000,
		UpdateRateHz: 10,
		Seed:         42,
		BoundsMinLat: -60,
		BoundsMaxLat: 70,
		BoundsMinLon: -180,
		BoundsMaxLon: 180,
		CellSizeDeg:  1.0,
	}
}

// Generator produces synthetic telemetry at a configurable rate.
type Generator struct {
	config   Config
	entities []entityInternal
	rng      *rand.Rand
	sequence atomic.Uint64
	started  time.Time
}

type entityInternal struct {
	state   EntityState
	pattern MovementPattern
	// Movement parameters
	targetLat float64
	targetLon float64
	orbitCenterLat float64
	orbitCenterLon float64
	orbitRadius    float64
	orbitAngle     float64
}

// New creates a new Generator with the given configuration.
func New(cfg Config) *Generator {
	rng := rand.New(rand.NewSource(cfg.Seed))

	g := &Generator{
		config:   cfg,
		entities: make([]entityInternal, cfg.EntityCount),
		rng:      rng,
		started:  time.Now(),
	}

	g.initEntities()
	return g
}

func (g *Generator) initEntities() {
	for i := range g.entities {
		e := &g.entities[i]

		// Assign entity type with distribution
		typeRoll := g.rng.Float64()
		switch {
		case typeRoll < 0.4:
			e.state.EntityType = TypeAircraft
		case typeRoll < 0.6:
			e.state.EntityType = TypeVessel
		case typeRoll < 0.75:
			e.state.EntityType = TypeVehicle
		case typeRoll < 0.85:
			e.state.EntityType = TypeDrone
		default:
			e.state.EntityType = TypeSensor
		}

		// Random initial position
		lat := g.config.BoundsMinLat + g.rng.Float64()*(g.config.BoundsMaxLat-g.config.BoundsMinLat)
		lon := g.config.BoundsMinLon + g.rng.Float64()*(g.config.BoundsMaxLon-g.config.BoundsMinLon)

		e.state.EntityID = uint32(i + 1)
		e.state.Flags = FlagActive | FlagPositionValid | FlagSpeedValid | FlagHeadingValid
		e.state.Latitude = lat
		e.state.Longitude = lon
		e.state.GridCell = ComputeGridCell(lat, lon, g.config.CellSizeDeg)

		// Type-specific initialization
		switch e.state.EntityType {
		case TypeAircraft:
			e.state.Flags |= FlagAltitudeValid | FlagVRateValid
			e.state.AltitudeM = 3000 + g.rng.Float32()*10000 // 3km - 13km
			e.state.SpeedMs = 150 + g.rng.Float32()*150      // 150-300 m/s
			e.state.HeadingDeg = g.rng.Float32() * 360
			e.pattern = PatternGreatCircle
			// Set random target
			e.targetLat = g.config.BoundsMinLat + g.rng.Float64()*(g.config.BoundsMaxLat-g.config.BoundsMinLat)
			e.targetLon = g.config.BoundsMinLon + g.rng.Float64()*(g.config.BoundsMaxLon-g.config.BoundsMinLon)

		case TypeVessel:
			e.state.AltitudeM = 0
			e.state.SpeedMs = 2 + g.rng.Float32()*13 // 2-15 m/s (~4-30 knots)
			e.state.HeadingDeg = g.rng.Float32() * 360
			e.pattern = PatternGreatCircle
			e.targetLat = g.config.BoundsMinLat + g.rng.Float64()*(g.config.BoundsMaxLat-g.config.BoundsMinLat)
			e.targetLon = g.config.BoundsMinLon + g.rng.Float64()*(g.config.BoundsMaxLon-g.config.BoundsMinLon)

		case TypeVehicle:
			e.state.AltitudeM = 0
			e.state.SpeedMs = 5 + g.rng.Float32()*30 // 5-35 m/s (~18-126 km/h)
			e.state.HeadingDeg = g.rng.Float32() * 360
			e.pattern = PatternRandomWalk

		case TypeDrone:
			e.state.Flags |= FlagAltitudeValid | FlagVRateValid
			e.state.AltitudeM = 50 + g.rng.Float32()*200 // 50-250m
			e.state.SpeedMs = 5 + g.rng.Float32()*25     // 5-30 m/s
			e.pattern = PatternOrbit
			e.orbitCenterLat = lat
			e.orbitCenterLon = lon
			e.orbitRadius = 0.001 + g.rng.Float64()*0.01 // degrees
			e.orbitAngle = g.rng.Float64() * 2 * math.Pi

		case TypeSensor:
			e.state.SpeedMs = 0
			e.state.HeadingDeg = 0
			e.pattern = PatternStationary
		}
	}
}

// Tick advances all entities by one time step and returns the updated states.
func (g *Generator) Tick() []EntityState {
	now := uint64(time.Now().UnixMilli())
	dt := 1.0 / g.config.UpdateRateHz // seconds per tick
	seq := uint32(g.sequence.Add(1))

	states := make([]EntityState, len(g.entities))
	for i := range g.entities {
		g.updateEntity(&g.entities[i], dt, now, seq)
		states[i] = g.entities[i].state
	}
	return states
}

// TickInto advances entities and writes state into the provided slice (zero-alloc path).
func (g *Generator) TickInto(states []EntityState) int {
	now := uint64(time.Now().UnixMilli())
	dt := 1.0 / g.config.UpdateRateHz
	seq := uint32(g.sequence.Add(1))

	n := min(len(states), len(g.entities))
	for i := 0; i < n; i++ {
		g.updateEntity(&g.entities[i], dt, now, seq)
		states[i] = g.entities[i].state
	}
	return n
}

func (g *Generator) updateEntity(e *entityInternal, dt float64, now uint64, seq uint32) {
	e.state.TimestampMs = now
	e.state.Sequence = seq

	switch e.pattern {
	case PatternGreatCircle:
		g.moveGreatCircle(e, dt)
	case PatternRandomWalk:
		g.moveRandomWalk(e, dt)
	case PatternOrbit:
		g.moveOrbit(e, dt)
	case PatternStationary:
		// No movement
	}

	// Update grid cell
	e.state.GridCell = ComputeGridCell(e.state.Latitude, e.state.Longitude, g.config.CellSizeDeg)

	// Wrap longitude
	if e.state.Longitude > 180 {
		e.state.Longitude -= 360
	} else if e.state.Longitude < -180 {
		e.state.Longitude += 360
	}

	// Clamp latitude
	if e.state.Latitude > 89 {
		e.state.Latitude = 89
		e.state.HeadingDeg = 180
	} else if e.state.Latitude < -89 {
		e.state.Latitude = -89
		e.state.HeadingDeg = 0
	}
}

func (g *Generator) moveGreatCircle(e *entityInternal, dt float64) {
	// Simple great-circle approximation: move toward target
	dLat := e.targetLat - e.state.Latitude
	dLon := e.targetLon - e.state.Longitude

	dist := math.Sqrt(dLat*dLat + dLon*dLon)
	if dist < 0.1 {
		// Arrived — pick new target
		e.targetLat = g.config.BoundsMinLat + g.rng.Float64()*(g.config.BoundsMaxLat-g.config.BoundsMinLat)
		e.targetLon = g.config.BoundsMinLon + g.rng.Float64()*(g.config.BoundsMaxLon-g.config.BoundsMinLon)
		return
	}

	// Heading toward target
	heading := math.Atan2(dLon, dLat) * 180.0 / math.Pi
	if heading < 0 {
		heading += 360
	}
	e.state.HeadingDeg = float32(heading)

	// Move based on speed (approximate: 1 degree ≈ 111km at equator)
	speedDeg := float64(e.state.SpeedMs) * dt / 111000.0
	e.state.Latitude += speedDeg * math.Cos(float64(e.state.HeadingDeg)*math.Pi/180.0)
	e.state.Longitude += speedDeg * math.Sin(float64(e.state.HeadingDeg)*math.Pi/180.0) / math.Cos(e.state.Latitude*math.Pi/180.0)
}

func (g *Generator) moveRandomWalk(e *entityInternal, dt float64) {
	// Small random heading changes
	e.state.HeadingDeg += float32(g.rng.NormFloat64() * 5) // ±5° per tick
	if e.state.HeadingDeg < 0 {
		e.state.HeadingDeg += 360
	}
	if e.state.HeadingDeg >= 360 {
		e.state.HeadingDeg -= 360
	}

	// Small speed variations
	e.state.SpeedMs += float32(g.rng.NormFloat64() * 0.5)
	if e.state.SpeedMs < 1 {
		e.state.SpeedMs = 1
	}
	if e.state.SpeedMs > 40 {
		e.state.SpeedMs = 40
	}

	speedDeg := float64(e.state.SpeedMs) * dt / 111000.0
	headRad := float64(e.state.HeadingDeg) * math.Pi / 180.0
	e.state.Latitude += speedDeg * math.Cos(headRad)
	e.state.Longitude += speedDeg * math.Sin(headRad) / math.Cos(e.state.Latitude*math.Pi/180.0)
}

func (g *Generator) moveOrbit(e *entityInternal, dt float64) {
	// Circular orbit around center point
	angularSpeed := float64(e.state.SpeedMs) / (e.orbitRadius * 111000.0) // rad/s
	e.orbitAngle += angularSpeed * dt

	e.state.Latitude = e.orbitCenterLat + e.orbitRadius*math.Cos(e.orbitAngle)
	e.state.Longitude = e.orbitCenterLon + e.orbitRadius*math.Sin(e.orbitAngle)/math.Cos(e.orbitCenterLat*math.Pi/180.0)

	heading := math.Atan2(math.Cos(e.orbitAngle), -math.Sin(e.orbitAngle)) * 180.0 / math.Pi
	if heading < 0 {
		heading += 360
	}
	e.state.HeadingDeg = float32(heading)
}

// EncodeFrame encodes a complete frame (header + entities) into a byte buffer.
func EncodeFrame(entities []EntityState) []byte {
	count := len(entities)
	buf := make([]byte, FrameHeaderSize+count*EntityStateSize)

	header := FrameHeader{
		Magic:       FrameMagic,
		Version:     ProtocolVersion,
		FrameType:   0, // full frame
		EntityCount: uint16(count),
		TimestampMs: uint64(time.Now().UnixMilli()),
	}
	MarshalFrameHeader(&header, buf[:FrameHeaderSize])

	for i, e := range entities {
		offset := FrameHeaderSize + i*EntityStateSize
		e.MarshalBinary(buf[offset : offset+EntityStateSize])
	}

	return buf
}

// EncodeFrameInto encodes into a pre-allocated buffer. Returns bytes written.
func EncodeFrameInto(entities []EntityState, buf []byte) int {
	count := len(entities)
	needed := FrameHeaderSize + count*EntityStateSize
	if len(buf) < needed {
		return 0
	}

	header := FrameHeader{
		Magic:       FrameMagic,
		Version:     ProtocolVersion,
		FrameType:   0,
		EntityCount: uint16(count),
		TimestampMs: uint64(time.Now().UnixMilli()),
	}
	MarshalFrameHeader(&header, buf[:FrameHeaderSize])

	for i, e := range entities {
		offset := FrameHeaderSize + i*EntityStateSize
		e.MarshalBinary(buf[offset : offset+EntityStateSize])
	}

	return needed
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
