// Command edge runs the StreamGrid edge server with real-world feeds.
//
// Connects to ADS-B Beast and AIS NMEA TCP streams and broadcasts
// decoded entity state to browser clients via WebSocket.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/streamgrid/streamgrid/edge"
	"github.com/streamgrid/streamgrid/generator"
)

func main() {
	var (
		wsAddr    = flag.String("ws-addr", ":8080", "WebSocket listen address")
		wtAddr    = flag.String("wt-addr", ":4433", "WebTransport listen address")
		rate      = flag.Float64("rate", 2, "Update rate Hz")
		beastAddr = flag.String("beast", "", "Beast TCP feed address (host:port)")
		nmeaAddr  = flag.String("nmea", "", "NMEA TCP feed address (host:port), comma-separated for multiple")
		certFile  = flag.String("cert", "", "TLS certificate file")
		keyFile   = flag.String("key", "", "TLS key file")
		fallback  = flag.Int("fallback-entities", 0, "Synthetic fallback entity count (0=disabled)")
	)
	flag.Parse()

	// Configure edge server
	cfg := edge.DefaultConfig()
	cfg.WSAddr = *wsAddr
	cfg.WTAddr = *wtAddr
	cfg.CertFile = *certFile
	cfg.KeyFile = *keyFile

	server := edge.New(cfg)

	// Configure pipeline
	pipeCfg := edge.PipelineConfig{
		UpdateRateHz:   *rate,
		MaxEntities:    100000,
		ReconnectDelay: 5 * time.Second,
	}

	// Optional synthetic fallback
	if *fallback > 0 {
		genCfg := generator.DefaultConfig()
		genCfg.EntityCount = *fallback
		pipeCfg.FallbackGenerator = generator.New(genCfg)
	}

	pipeline := edge.NewPipeline(server, pipeCfg)

	// Add feeds
	if *beastAddr != "" {
		log.Printf("Adding Beast feed: %s", *beastAddr)
		pipeline.AddADSBFeed(*beastAddr)
	}
	if *nmeaAddr != "" {
		for _, addr := range strings.Split(*nmeaAddr, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				log.Printf("Adding NMEA feed: %s", addr)
				pipeline.AddAISFeed(addr)
			}
		}
	}

	log.Printf("StreamGrid Edge Server starting")
	log.Printf("  WebSocket: %s", cfg.WSAddr)
	log.Printf("  Update rate: %.1f Hz", *rate)

	// Start server
	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Start pipeline
	ctx, cancel := context.WithCancel(context.Background())
	go pipeline.Start(ctx)

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	cancel()
	server.Stop()
}
