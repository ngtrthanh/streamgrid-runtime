//go:build integration

package edge

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/streamgrid/streamgrid/generator"
)

// TestScalingExperiment runs the full pipeline at increasing entity counts
// and measures throughput, latency, and resource usage.
func TestScalingExperiment(t *testing.T) {
	entityCounts := []int{100, 1000, 10000, 100000}

	for _, count := range entityCounts {
		t.Run(fmt.Sprintf("%d_entities", count), func(t *testing.T) {
			runScalingTest(t, count)
		})
	}
}

func runScalingTest(t *testing.T, entityCount int) {
	// Start edge server on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cfg := DefaultConfig()
	cfg.WSAddr = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.MaxBufferFrames = 30
	server := New(cfg)

	// Start generator
	genCfg := generator.DefaultConfig()
	genCfg.EntityCount = entityCount
	genCfg.UpdateRateHz = 10
	genCfg.Seed = 42
	gen := generator.New(genCfg)

	// Set up WebSocket handler
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		server.handleWebSocketClient(conn)
	})

	httpServer := &http.Server{Addr: cfg.WSAddr, Handler: mux}
	go httpServer.ListenAndServe()
	defer httpServer.Close()
	time.Sleep(50 * time.Millisecond)

	// Start frame generation
	var genRunning atomic.Bool
	genRunning.Store(true)
	states := make([]generator.EntityState, entityCount)
	buf := make([]byte, generator.FrameHeaderSize+entityCount*generator.EntityStateSize)

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond) // 10 Hz
		defer ticker.Stop()
		for range ticker.C {
			if !genRunning.Load() {
				return
			}
			gen.TickInto(states)
			n := generator.EncodeFrameInto(states, buf)
			server.BroadcastFrame(buf[:n])
		}
	}()

	// Connect client
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	// Receive frames and measure
	const measureFrames = 10
	var latencies []time.Duration
	var frameSizes []int
	var frameDecodeErrors int
	receiveStart := time.Now()

	for i := 0; i < measureFrames; i++ {
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		frameStart := time.Now()
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		latency := time.Since(frameStart)
		latencies = append(latencies, latency)
		frameSizes = append(frameSizes, len(msg))

		// Validate frame
		if len(msg) < generator.FrameHeaderSize {
			frameDecodeErrors++
			continue
		}
		magic := binary.LittleEndian.Uint32(msg[0:4])
		if magic != generator.FrameMagic {
			frameDecodeErrors++
			continue
		}
		entityCountInFrame := binary.LittleEndian.Uint16(msg[6:8])
		expectedSize := generator.FrameHeaderSize + int(entityCountInFrame)*generator.EntityStateSize
		if len(msg) < expectedSize {
			frameDecodeErrors++
			continue
		}

		// Validate first entity
		offset := generator.FrameHeaderSize
		lat := math.Float64frombits(binary.LittleEndian.Uint64(msg[offset+16 : offset+24]))
		lon := math.Float64frombits(binary.LittleEndian.Uint64(msg[offset+24 : offset+32]))
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			frameDecodeErrors++
		}
	}

	totalReceiveTime := time.Since(receiveStart)
	genRunning.Store(false)

	// Calculate statistics
	var sumLatency time.Duration
	minLatency := latencies[0]
	maxLatency := latencies[0]
	for _, l := range latencies {
		sumLatency += l
		if l < minLatency {
			minLatency = l
		}
		if l > maxLatency {
			maxLatency = l
		}
	}
	avgLatency := sumLatency / time.Duration(len(latencies))
	avgFrameSize := 0
	for _, s := range frameSizes {
		avgFrameSize += s
	}
	avgFrameSize /= len(frameSizes)

	fps := float64(measureFrames) / totalReceiveTime.Seconds()
	throughputMBs := float64(avgFrameSize) * fps / 1024 / 1024

	// Report
	t.Logf("=== Scaling Results: %d entities ===", entityCount)
	t.Logf("  Frames received:    %d", measureFrames)
	t.Logf("  Decode errors:      %d", frameDecodeErrors)
	t.Logf("  Frame size:         %d bytes (%.1f KB)", avgFrameSize, float64(avgFrameSize)/1024)
	t.Logf("  Effective FPS:      %.1f", fps)
	t.Logf("  Throughput:         %.2f MB/s", throughputMBs)
	t.Logf("  Latency (min):      %s", minLatency)
	t.Logf("  Latency (avg):      %s", avgLatency)
	t.Logf("  Latency (max):      %s", maxLatency)

	// Assertions
	if frameDecodeErrors > 0 {
		t.Errorf("%d frame decode errors", frameDecodeErrors)
	}
	if avgFrameSize != generator.FrameHeaderSize+entityCount*generator.EntityStateSize {
		t.Errorf("expected frame size %d, got %d",
			generator.FrameHeaderSize+entityCount*generator.EntityStateSize, avgFrameSize)
	}
}

// TestScalingMultiClient tests broadcasting to multiple concurrent clients.
func TestScalingMultiClient(t *testing.T) {
	const entityCount = 1000
	const numClients = 10
	const measureFrames = 5

	// Start edge server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cfg := DefaultConfig()
	cfg.WSAddr = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.MaxBufferFrames = 30
	server := New(cfg)

	genCfg := generator.DefaultConfig()
	genCfg.EntityCount = entityCount
	genCfg.UpdateRateHz = 10
	genCfg.Seed = 42
	gen := generator.New(genCfg)

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		server.handleWebSocketClient(conn)
	})

	httpServer := &http.Server{Addr: cfg.WSAddr, Handler: mux}
	go httpServer.ListenAndServe()
	defer httpServer.Close()
	time.Sleep(50 * time.Millisecond)

	// Start generating
	var genRunning atomic.Bool
	genRunning.Store(true)
	states := make([]generator.EntityState, entityCount)
	buf := make([]byte, generator.FrameHeaderSize+entityCount*generator.EntityStateSize)

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if !genRunning.Load() {
				return
			}
			gen.TickInto(states)
			n := generator.EncodeFrameInto(states, buf)
			server.BroadcastFrame(buf[:n])
		}
	}()

	// Connect N clients concurrently
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	var wg sync.WaitGroup
	var totalFramesReceived atomic.Int64

	for c := 0; c < numClients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Errorf("client %d dial: %v", clientID, err)
				return
			}
			defer ws.Close()

			for i := 0; i < measureFrames; i++ {
				ws.SetReadDeadline(time.Now().Add(5 * time.Second))
				_, _, err := ws.ReadMessage()
				if err != nil {
					t.Errorf("client %d read: %v", clientID, err)
					return
				}
				totalFramesReceived.Add(1)
			}
		}(c)
	}

	wg.Wait()
	genRunning.Store(false)

	expectedFrames := int64(numClients * measureFrames)
	actual := totalFramesReceived.Load()

	t.Logf("=== Multi-Client Results ===")
	t.Logf("  Clients:           %d", numClients)
	t.Logf("  Expected frames:   %d", expectedFrames)
	t.Logf("  Received frames:   %d", actual)
	t.Logf("  Server clients:    %d (peak)", numClients)

	if actual < expectedFrames {
		t.Errorf("expected %d total frames, got %d", expectedFrames, actual)
	}
}
