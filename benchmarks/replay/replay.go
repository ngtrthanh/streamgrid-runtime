// Package replay provides recording and playback of StreamGrid telemetry frames.
//
// Recordings are stored in a simple binary format:
//   [8 bytes: timestamp_ns since recording start][4 bytes: frame_length][frame_data...]
//
// This enables deterministic replay at original speed or accelerated.
package replay

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	// RecordHeaderSize is the per-frame record header: timestamp(8) + length(4)
	RecordHeaderSize = 12

	// FileMagic identifies replay files.
	FileMagic uint64 = 0x5347_5250_4C41_5900 // "SGRPLAY\0"

	// FileHeaderSize is the file header size.
	FileHeaderSize = 32
)

// FileHeader is written at the start of a replay file.
type FileHeader struct {
	Magic       uint64
	Version     uint8
	_           [3]byte // padding
	EntityCount uint32
	UpdateRate  float32
	StartTimeNs uint64
	_           [4]byte // reserved
}

// Recorder captures telemetry frames with timestamps for later replay.
type Recorder struct {
	w           io.Writer
	startTime   time.Time
	frameCount  uint64
	bytesWritten uint64
}

// NewRecorder creates a new Recorder writing to the given writer.
func NewRecorder(w io.Writer, entityCount int, updateRate float64) (*Recorder, error) {
	r := &Recorder{
		w:         w,
		startTime: time.Now(),
	}

	// Write file header
	header := make([]byte, FileHeaderSize)
	binary.LittleEndian.PutUint64(header[0:8], FileMagic)
	header[8] = 1 // version
	binary.LittleEndian.PutUint32(header[12:16], uint32(entityCount))
	binary.LittleEndian.PutUint32(header[16:20], uint32(updateRate)) // simplified
	binary.LittleEndian.PutUint64(header[20:28], uint64(r.startTime.UnixNano()))

	if _, err := w.Write(header); err != nil {
		return nil, fmt.Errorf("write file header: %w", err)
	}
	r.bytesWritten = FileHeaderSize

	return r, nil
}

// RecordFrame records a single frame with its timestamp offset.
func (r *Recorder) RecordFrame(frameData []byte) error {
	elapsed := time.Since(r.startTime)

	// Write record header: [8 bytes elapsed_ns][4 bytes frame_len]
	recordHeader := make([]byte, RecordHeaderSize)
	binary.LittleEndian.PutUint64(recordHeader[0:8], uint64(elapsed.Nanoseconds()))
	binary.LittleEndian.PutUint32(recordHeader[8:12], uint32(len(frameData)))

	if _, err := r.w.Write(recordHeader); err != nil {
		return fmt.Errorf("write record header: %w", err)
	}
	if _, err := r.w.Write(frameData); err != nil {
		return fmt.Errorf("write frame data: %w", err)
	}

	r.frameCount++
	r.bytesWritten += uint64(RecordHeaderSize + len(frameData))
	return nil
}

// Stats returns recording statistics.
func (r *Recorder) Stats() (frames uint64, bytes uint64, duration time.Duration) {
	return r.frameCount, r.bytesWritten, time.Since(r.startTime)
}

// Player replays recorded telemetry frames.
type Player struct {
	r           io.Reader
	header      FileHeader
	speed       float64
	startTime   time.Time
	started     bool
	frameCount  uint64
}

// NewPlayer creates a new Player reading from the given reader.
func NewPlayer(r io.Reader) (*Player, error) {
	p := &Player{
		r:     r,
		speed: 1.0,
	}

	// Read file header
	headerBuf := make([]byte, FileHeaderSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, fmt.Errorf("read file header: %w", err)
	}

	magic := binary.LittleEndian.Uint64(headerBuf[0:8])
	if magic != FileMagic {
		return nil, fmt.Errorf("invalid magic: 0x%X", magic)
	}

	p.header.Magic = magic
	p.header.Version = headerBuf[8]
	p.header.EntityCount = binary.LittleEndian.Uint32(headerBuf[12:16])
	p.header.StartTimeNs = binary.LittleEndian.Uint64(headerBuf[20:28])

	return p, nil
}

// SetSpeed sets the playback speed multiplier (1.0 = original speed, 2.0 = double speed).
func (p *Player) SetSpeed(speed float64) {
	if speed <= 0 {
		speed = 1.0
	}
	p.speed = speed
}

// EntityCount returns the number of entities in the recording.
func (p *Player) EntityCount() uint32 {
	return p.header.EntityCount
}

// NextFrame reads and returns the next frame. If waitForTiming is true,
// it sleeps to maintain original timing (adjusted by speed).
// Returns io.EOF when no more frames.
func (p *Player) NextFrame(waitForTiming bool) ([]byte, error) {
	// Read record header
	recordHeader := make([]byte, RecordHeaderSize)
	if _, err := io.ReadFull(p.r, recordHeader); err != nil {
		return nil, err
	}

	elapsedNs := binary.LittleEndian.Uint64(recordHeader[0:8])
	frameLen := binary.LittleEndian.Uint32(recordHeader[8:12])

	// Read frame data
	frameData := make([]byte, frameLen)
	if _, err := io.ReadFull(p.r, frameData); err != nil {
		return nil, fmt.Errorf("read frame data: %w", err)
	}

	// Wait for timing
	if waitForTiming {
		if !p.started {
			p.startTime = time.Now()
			p.started = true
		} else {
			targetElapsed := time.Duration(float64(elapsedNs) / p.speed)
			actualElapsed := time.Since(p.startTime)
			if targetElapsed > actualElapsed {
				time.Sleep(targetElapsed - actualElapsed)
			}
		}
	}

	p.frameCount++
	return frameData, nil
}

// FrameCount returns the number of frames read so far.
func (p *Player) FrameCount() uint64 {
	return p.frameCount
}

// RecordToFile is a convenience function to record generator output to a file.
func RecordToFile(path string, entityCount int, updateRate float64, duration time.Duration, genTick func() []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	rec, err := NewRecorder(f, entityCount, updateRate)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(time.Duration(float64(time.Second) / updateRate))
	defer ticker.Stop()

	deadline := time.After(duration)

	for {
		select {
		case <-ticker.C:
			frameData := genTick()
			if err := rec.RecordFrame(frameData); err != nil {
				return err
			}
		case <-deadline:
			frames, bytes, dur := rec.Stats()
			fmt.Printf("Recording complete: %d frames, %d bytes, %s\n", frames, bytes, dur.Round(time.Millisecond))
			return nil
		}
	}
}

// PlayFromFile is a convenience function to play back a recording.
func PlayFromFile(path string, speed float64, onFrame func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	player, err := NewPlayer(f)
	if err != nil {
		return err
	}
	player.SetSpeed(speed)

	for {
		frame, err := player.NextFrame(speed > 0)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := onFrame(frame); err != nil {
			return err
		}
	}
}
