package replay

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestRecordAndPlayback(t *testing.T) {
	var buf bytes.Buffer

	// Record
	rec, err := NewRecorder(&buf, 100, 10)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	// Record 5 frames
	for i := 0; i < 5; i++ {
		frame := make([]byte, 64*10) // 10 entities worth
		frame[0] = byte(i)           // marker
		if err := rec.RecordFrame(frame); err != nil {
			t.Fatalf("RecordFrame %d: %v", i, err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	frames, bytesWritten, dur := rec.Stats()
	if frames != 5 {
		t.Errorf("expected 5 frames, got %d", frames)
	}
	if bytesWritten == 0 {
		t.Error("expected non-zero bytes written")
	}
	if dur == 0 {
		t.Error("expected non-zero duration")
	}

	// Play back (no timing)
	reader := bytes.NewReader(buf.Bytes())
	player, err := NewPlayer(reader)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}

	if player.EntityCount() != 100 {
		t.Errorf("expected 100 entities, got %d", player.EntityCount())
	}

	var playedFrames int
	for {
		frame, err := player.NextFrame(false)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextFrame: %v", err)
		}
		if frame[0] != byte(playedFrames) {
			t.Errorf("frame %d: expected marker %d, got %d", playedFrames, playedFrames, frame[0])
		}
		playedFrames++
	}

	if playedFrames != 5 {
		t.Errorf("expected 5 played frames, got %d", playedFrames)
	}
	if player.FrameCount() != 5 {
		t.Errorf("expected FrameCount 5, got %d", player.FrameCount())
	}
}

func TestPlayerInvalidMagic(t *testing.T) {
	data := make([]byte, FileHeaderSize)
	reader := bytes.NewReader(data)
	_, err := NewPlayer(reader)
	if err == nil {
		t.Error("expected error for invalid magic")
	}
}

func TestPlayerSpeed(t *testing.T) {
	var buf bytes.Buffer

	rec, err := NewRecorder(&buf, 10, 10)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	// Record 3 frames with delays
	for i := 0; i < 3; i++ {
		frame := []byte{byte(i)}
		rec.RecordFrame(frame)
		time.Sleep(10 * time.Millisecond)
	}

	// Play at 10x speed — should finish quickly
	reader := bytes.NewReader(buf.Bytes())
	player, err := NewPlayer(reader)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	player.SetSpeed(10.0)

	start := time.Now()
	for {
		_, err := player.NextFrame(true)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextFrame: %v", err)
		}
	}
	elapsed := time.Since(start)

	// At 10x speed, ~30ms of recording should take ~3ms
	if elapsed > 20*time.Millisecond {
		t.Logf("warning: 10x playback took %s (expected <20ms)", elapsed)
	}
}
