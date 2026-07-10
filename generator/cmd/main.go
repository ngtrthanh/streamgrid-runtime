// Command generator runs the StreamGrid synthetic telemetry generator.
//
// It produces a configurable stream of moving entities at a specified update rate
// and outputs binary frames to stdout or a network listener.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/streamgrid/streamgrid/generator"
)

func main() {
	var (
		entityCount  = flag.Int("entities", 1000, "Number of entities to generate")
		updateRate   = flag.Float64("rate", 10, "Update rate in Hz")
		seed         = flag.Int64("seed", 42, "Random seed for deterministic generation")
		listenAddr   = flag.String("listen", ":8081", "TCP listen address for frame output")
		mode         = flag.String("mode", "tcp", "Output mode: tcp, stdout, bench")
		benchSeconds = flag.Float64("bench-seconds", 5, "Duration for bench mode")
	)
	flag.Parse()

	cfg := generator.DefaultConfig()
	cfg.EntityCount = *entityCount
	cfg.UpdateRateHz = *updateRate
	cfg.Seed = *seed

	log.Printf("StreamGrid Generator starting: %d entities @ %.1f Hz, seed=%d",
		cfg.EntityCount, cfg.UpdateRateHz, cfg.Seed)

	gen := generator.New(cfg)

	switch *mode {
	case "tcp":
		runTCPServer(gen, cfg, *listenAddr)
	case "stdout":
		runStdout(gen, cfg)
	case "bench":
		runBench(gen, cfg, *benchSeconds)
	default:
		log.Fatalf("unknown mode: %s", *mode)
	}
}

func runTCPServer(gen *generator.Generator, cfg generator.Config, addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("Listening on %s", addr)

	var clients sync.Map
	var clientCount atomic.Int64

	// Accept connections
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("accept: %v", err)
				return
			}
			clientCount.Add(1)
			clients.Store(conn, true)
			log.Printf("Client connected: %s (total: %d)", conn.RemoteAddr(), clientCount.Load())
		}
	}()

	// Generate and broadcast
	ticker := time.NewTicker(time.Duration(float64(time.Second) / cfg.UpdateRateHz))
	defer ticker.Stop()

	// Pre-allocate buffer for zero-alloc frame encoding
	buf := make([]byte, generator.FrameHeaderSize+cfg.EntityCount*generator.EntityStateSize)
	states := make([]generator.EntityState, cfg.EntityCount)

	var frameCount uint64
	statsInterval := time.NewTicker(5 * time.Second)
	defer statsInterval.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			gen.TickInto(states)
			n := generator.EncodeFrameInto(states, buf)
			frameCount++

			// Broadcast to all connected clients
			clients.Range(func(key, value interface{}) bool {
				conn := key.(net.Conn)
				conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				_, err := conn.Write(buf[:n])
				if err != nil {
					conn.Close()
					clients.Delete(key)
					clientCount.Add(-1)
					log.Printf("Client disconnected: %s (total: %d)", conn.RemoteAddr(), clientCount.Load())
				}
				return true
			})

		case <-statsInterval.C:
			log.Printf("Stats: frames=%d, clients=%d, entities=%d",
				frameCount, clientCount.Load(), cfg.EntityCount)

		case <-sigCh:
			log.Println("Shutting down...")
			ln.Close()
			return
		}
	}
}

func runStdout(gen *generator.Generator, cfg generator.Config) {
	ticker := time.NewTicker(time.Duration(float64(time.Second) / cfg.UpdateRateHz))
	defer ticker.Stop()

	buf := make([]byte, generator.FrameHeaderSize+cfg.EntityCount*generator.EntityStateSize)
	states := make([]generator.EntityState, cfg.EntityCount)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			gen.TickInto(states)
			n := generator.EncodeFrameInto(states, buf)
			os.Stdout.Write(buf[:n])

		case <-sigCh:
			return
		}
	}
}

func runBench(gen *generator.Generator, cfg generator.Config, seconds float64) {
	log.Printf("Running benchmark for %.1f seconds...", seconds)

	states := make([]generator.EntityState, cfg.EntityCount)
	buf := make([]byte, generator.FrameHeaderSize+cfg.EntityCount*generator.EntityStateSize)

	start := time.Now()
	deadline := start.Add(time.Duration(seconds * float64(time.Second)))
	frames := 0

	for time.Now().Before(deadline) {
		gen.TickInto(states)
		generator.EncodeFrameInto(states, buf)
		frames++
	}

	elapsed := time.Since(start)
	fps := float64(frames) / elapsed.Seconds()
	entitiesPerSec := fps * float64(cfg.EntityCount)
	mbPerSec := fps * float64(len(buf)) / 1024 / 1024

	fmt.Printf("=== Generator Benchmark ===\n")
	fmt.Printf("  Entities:       %d\n", cfg.EntityCount)
	fmt.Printf("  Duration:       %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Frames:         %d\n", frames)
	fmt.Printf("  Frame rate:     %.1f fps\n", fps)
	fmt.Printf("  Entity updates: %.0f /s\n", entitiesPerSec)
	fmt.Printf("  Throughput:     %.1f MB/s\n", mbPerSec)
	fmt.Printf("  Frame size:     %d bytes (%.1f KB)\n", len(buf), float64(len(buf))/1024)
}
