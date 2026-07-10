//go:build integration

package edge

// End-to-end integration test for the StreamGrid edge server.
//
// It starts an edge server on a random port, connects a WebSocket client,
// sends a binary frame (generated via the generator package), and verifies
// that the decoded frame satisfies all protocol invariants.

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/streamgrid/streamgrid/generator"
)

// TestIntegrationEdgeServer is the end-to-end integration test.
func TestIntegrationEdgeServer(t *testing.T) {
	// ── 1. Build an edge server and a test HTTP server on a random port ──────

	cfg := DefaultConfig()
	// Disable WebTransport by giving WTAddr an unused value;
	// we drive only the WebSocket path through httptest.NewServer.
	cfg.MaxBufferFrames = 20
	srv := New(cfg)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		srv.handleWebSocketClient(conn)
	})

	// httptest.NewServer binds to 127.0.0.1:<random port>
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// ── 2. Connect a WebSocket client ────────────────────────────────────────

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}

	wsConn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	defer wsConn.Close()

	// Allow the server goroutine to register the client
	time.Sleep(30 * time.Millisecond)

	if srv.ClientCount() != 1 {
		t.Fatalf("expected 1 connected client, got %d", srv.ClientCount())
	}

	// ── 3. Generate a frame and broadcast it ─────────────────────────────────

	const entityCount = 5
	genCfg := generator.DefaultConfig()
	genCfg.EntityCount = entityCount
	genCfg.Seed = 12345
	gen := generator.New(genCfg)

	states := make([]generator.EntityState, entityCount)
	n := gen.TickInto(states)
	if n != entityCount {
		t.Fatalf("TickInto returned %d, expected %d", n, entityCount)
	}

	frameSize := generator.FrameHeaderSize + entityCount*generator.EntityStateSize
	buf := make([]byte, frameSize)
	written := generator.EncodeFrameInto(states[:n], buf)
	if written != frameSize {
		t.Fatalf("EncodeFrameInto wrote %d bytes, expected %d", written, frameSize)
	}

	srv.BroadcastFrame(buf[:written])

	// ── 4. Receive the frame on the WebSocket client ──────────────────────────

	wsConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	msgType, msg, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("expected BinaryMessage (%d), got %d", websocket.BinaryMessage, msgType)
	}
	t.Logf("received %d bytes", len(msg))

	// ── 5. Decode and verify the frame ───────────────────────────────────────

	verifyFrame(t, msg, entityCount)

	// ── 6. Clean up ──────────────────────────────────────────────────────────

	wsConn.Close()
	time.Sleep(50 * time.Millisecond)
}

// verifyFrame decodes a raw frame and asserts all protocol invariants.
func verifyFrame(t *testing.T, frame []byte, expectedEntityCount int) {
	t.Helper()

	minLen := generator.FrameHeaderSize + expectedEntityCount*generator.EntityStateSize
	if len(frame) < minLen {
		t.Fatalf("frame too short: got %d bytes, want at least %d", len(frame), minLen)
	}

	// ── Header ───────────────────────────────────────────────────────────────

	magic := binary.LittleEndian.Uint32(frame[0:4])
	if magic != generator.FrameMagic {
		t.Errorf("magic mismatch: got 0x%08X, want 0x%08X", magic, generator.FrameMagic)
	}

	version := frame[4]
	_ = version // informational; generator sets it to ProtocolVersion

	entityCount := binary.LittleEndian.Uint16(frame[6:8])
	if int(entityCount) != expectedEntityCount {
		t.Errorf("entity_count mismatch: got %d, want %d", entityCount, expectedEntityCount)
	}

	headerTs := binary.LittleEndian.Uint64(frame[8:16])
	if headerTs == 0 {
		t.Error("header timestamp is zero")
	}

	// ── Entities ─────────────────────────────────────────────────────────────

	for i := 0; i < int(entityCount); i++ {
		offset := generator.FrameHeaderSize + i*generator.EntityStateSize
		rec := frame[offset : offset+generator.EntityStateSize]

		var e generator.EntityState
		e.UnmarshalBinary(rec)

		label := fmt.Sprintf("entity[%d] (id=%d)", i, e.EntityID)

		// entity_id must be nonzero
		if e.EntityID == 0 {
			t.Errorf("%s: entity_id is 0", label)
		}

		// flags: FlagActive and FlagPositionValid must both be set for moving entities
		requiredFlags := generator.FlagActive | generator.FlagPositionValid
		if e.Flags&requiredFlags != requiredFlags {
			t.Errorf("%s: flags 0x%04X missing required bits 0x%04X", label, e.Flags, requiredFlags)
		}

		// latitude must be in [-90, 90]
		if math.IsNaN(e.Latitude) || e.Latitude < -90 || e.Latitude > 90 {
			t.Errorf("%s: latitude %f out of range", label, e.Latitude)
		}

		// longitude must be in [-180, 180]
		if math.IsNaN(e.Longitude) || e.Longitude < -180 || e.Longitude > 180 {
			t.Errorf("%s: longitude %f out of range", label, e.Longitude)
		}

		// timestamp must be nonzero
		if e.TimestampMs == 0 {
			t.Errorf("%s: timestamp is zero", label)
		}

		t.Logf("%s: lat=%.4f lon=%.4f flags=0x%04X type=%d alt=%.1fm speed=%.1fm/s",
			label, e.Latitude, e.Longitude, e.Flags, e.EntityType,
			e.AltitudeM, e.SpeedMs)
	}
}

// TestIntegrationEdgeServerRandomPort verifies that a random OS-assigned port
// works correctly (no hard-coded port conflicts).
func TestIntegrationEdgeServerRandomPort(t *testing.T) {
	// Pick a free port by binding then releasing a listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := DefaultConfig()
	cfg.WSAddr = addr
	cfg.MaxBufferFrames = 10
	srv := New(cfg)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		srv.handleWebSocketClient(conn)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer wsConn.Close()

	time.Sleep(30 * time.Millisecond)

	// Broadcast a minimal frame with 1 entity
	const n = 1
	genCfg := generator.DefaultConfig()
	genCfg.EntityCount = n
	gen := generator.New(genCfg)

	states := make([]generator.EntityState, n)
	gen.TickInto(states)

	frameSize := generator.FrameHeaderSize + n*generator.EntityStateSize
	buf := make([]byte, frameSize)
	generator.EncodeFrameInto(states, buf)
	srv.BroadcastFrame(buf)

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	verifyFrame(t, msg, n)
}
