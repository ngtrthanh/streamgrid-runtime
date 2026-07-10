// Command edge runs the StreamGrid edge server.
//
// It connects to the generator (or runs an internal generator) and streams
// binary entity frames to connected browser clients via WebTransport/WebSocket.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/streamgrid/streamgrid/edge"
	"github.com/streamgrid/streamgrid/generator"
)

func main() {
	var (
		wsAddr       = flag.String("ws-addr", ":8080", "WebSocket listen address")
		wtAddr       = flag.String("wt-addr", ":4433", "WebTransport listen address")
		entityCount  = flag.Int("entities", 1000, "Entity count (internal generator)")
		updateRate   = flag.Float64("rate", 10, "Update rate Hz (internal generator)")
		seed         = flag.Int64("seed", 42, "Generator seed")
		certFile     = flag.String("cert", "", "TLS certificate file")
		keyFile      = flag.String("key", "", "TLS key file")
	)
	flag.Parse()

	// Configure edge server
	cfg := edge.DefaultConfig()
	cfg.WSAddr = *wsAddr
	cfg.WTAddr = *wtAddr
	cfg.CertFile = *certFile
	cfg.KeyFile = *keyFile

	server := edge.New(cfg)

	// Start internal generator
	genCfg := generator.DefaultConfig()
	genCfg.EntityCount = *entityCount
	genCfg.UpdateRateHz = *updateRate
	genCfg.Seed = *seed
	gen := generator.New(genCfg)

	log.Printf("StreamGrid Edge Server starting")
	log.Printf("  Entities: %d @ %.1f Hz", genCfg.EntityCount, genCfg.UpdateRateHz)
	log.Printf("  WebSocket: %s", cfg.WSAddr)
	log.Printf("  WebTransport: %s", cfg.WTAddr)

	// Start frame generation in background
	go func() {
		ticker := time.NewTicker(time.Duration(float64(time.Second) / genCfg.UpdateRateHz))
		defer ticker.Stop()

		states := make([]generator.EntityState, genCfg.EntityCount)
		buf := make([]byte, generator.FrameHeaderSize+genCfg.EntityCount*generator.EntityStateSize)

		for range ticker.C {
			gen.TickInto(states)
			n := generator.EncodeFrameInto(states, buf)
			server.BroadcastFrame(buf[:n])
		}
	}()

	// Start server in background
	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	server.Stop()
}
