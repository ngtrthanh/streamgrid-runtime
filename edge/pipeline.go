// Package edge - Pipeline wiring feeds into the edge server.
//
// The Pipeline collects entity states from ADS-B and AIS decoders (and
// optionally a synthetic generator), encodes them as StreamGrid binary frames,
// and broadcasts via the edge Server at a configurable rate.
package edge

import (
	"context"
	"log"
	"time"

	"github.com/streamgrid/streamgrid/applications/adsb"
	"github.com/streamgrid/streamgrid/applications/ais"
	"github.com/streamgrid/streamgrid/generator"
)

// PipelineConfig configures the pipeline.
type PipelineConfig struct {
	// UpdateRateHz is the frame broadcast rate (default 10 Hz).
	UpdateRateHz float64
	// ReconnectDelay for feed connectors (default 5s).
	ReconnectDelay time.Duration
	// MaxEntities is the upper bound on entities per frame (default 50000).
	MaxEntities int
	// FallbackGenerator is used when no live feeds are configured or have
	// produced data yet. Nil disables the fallback.
	FallbackGenerator *generator.Generator
}

// DefaultPipelineConfig returns sensible defaults.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		UpdateRateHz:   10,
		ReconnectDelay: 5 * time.Second,
		MaxEntities:    50000,
	}
}

// Pipeline wires one or more feed connectors and a synthetic generator into
// the edge Server, broadcasting binary frames at a fixed rate.
type Pipeline struct {
	cfg    PipelineConfig
	server *Server

	adsbFeeds []*adsbEntry
	aisFeeds  []*aisEntry

	// pre-allocated frame buffer
	frameBuf []byte
}

type adsbEntry struct {
	decoder *adsb.Decoder
	feed    *adsb.Feed
}

type aisEntry struct {
	decoder *ais.Decoder
	feed    *ais.Feed
}

// NewPipeline creates a new Pipeline using the given server and config.
func NewPipeline(srv *Server, cfg PipelineConfig) *Pipeline {
	if cfg.UpdateRateHz <= 0 {
		cfg.UpdateRateHz = 10
	}
	if cfg.MaxEntities <= 0 {
		cfg.MaxEntities = 50000
	}
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 5 * time.Second
	}

	maxFrameBytes := generator.FrameHeaderSize + cfg.MaxEntities*generator.EntityStateSize
	return &Pipeline{
		cfg:      cfg,
		server:   srv,
		frameBuf: make([]byte, maxFrameBytes),
	}
}

// AddADSBFeed registers a Beast TCP feed connector.
// A new Decoder is created for the feed automatically.
func (p *Pipeline) AddADSBFeed(addr string) *adsb.Decoder {
	dec := adsb.NewDecoder()
	feedCfg := adsb.FeedConfig{
		Addr:           addr,
		ReconnectDelay: p.cfg.ReconnectDelay,
	}
	feed := adsb.NewFeed(feedCfg, dec)
	p.adsbFeeds = append(p.adsbFeeds, &adsbEntry{decoder: dec, feed: feed})
	log.Printf("pipeline: registered ADS-B feed %s", addr)
	return dec
}

// AddAISFeed registers an NMEA AIS TCP feed connector.
// A new Decoder is created for the feed automatically.
func (p *Pipeline) AddAISFeed(addr string) *ais.Decoder {
	dec := ais.NewDecoder()
	feedCfg := ais.FeedConfig{
		Addr:           addr,
		ReconnectDelay: p.cfg.ReconnectDelay,
	}
	feed := ais.NewFeed(feedCfg, dec)
	p.aisFeeds = append(p.aisFeeds, &aisEntry{decoder: dec, feed: feed})
	log.Printf("pipeline: registered AIS feed %s", addr)
	return dec
}

// Start launches all feed connectors and begins broadcasting frames.
// It blocks until ctx is cancelled.
func (p *Pipeline) Start(ctx context.Context) {
	// Start feed connectors
	for _, entry := range p.adsbFeeds {
		go func(e *adsbEntry) {
			if err := e.feed.Connect(ctx); err != nil && ctx.Err() == nil {
				log.Printf("pipeline: ADS-B feed exited: %v", err)
			}
		}(entry)
	}
	for _, entry := range p.aisFeeds {
		go func(e *aisEntry) {
			if err := e.feed.Connect(ctx); err != nil && ctx.Err() == nil {
				log.Printf("pipeline: AIS feed exited: %v", err)
			}
		}(entry)
	}

	interval := time.Duration(float64(time.Second) / p.cfg.UpdateRateHz)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq uint32
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq++
			p.broadcastTick(seq)
		}
	}
}

// broadcastTick collects all current entity states and broadcasts one frame.
func (p *Pipeline) broadcastTick(seq uint32) {
	states := p.collectStates(seq)

	if len(states) == 0 {
		// Nothing to send
		return
	}

	// Cap at MaxEntities
	if len(states) > p.cfg.MaxEntities {
		states = states[:p.cfg.MaxEntities]
	}

	n := generator.EncodeFrameInto(states, p.frameBuf)
	if n == 0 {
		log.Printf("pipeline: EncodeFrameInto returned 0 (buffer too small?)")
		return
	}

	// Make a copy for BroadcastFrame (it sends to multiple clients concurrently)
	out := make([]byte, n)
	copy(out, p.frameBuf[:n])
	p.server.BroadcastFrame(out)
}

// collectStates gathers EntityState snapshots from all decoders and the
// optional fallback generator.
func (p *Pipeline) collectStates(seq uint32) []generator.EntityState {
	var states []generator.EntityState

	const maxAge = 60 * time.Second

	for _, entry := range p.adsbFeeds {
		// Prune stale aircraft
		entry.decoder.PruneStale()
		for _, ac := range entry.decoder.GetActiveAircraft(maxAge) {
			states = append(states, ac.ToEntityState(seq))
		}
	}

	for _, entry := range p.aisFeeds {
		// Prune stale vessels
		entry.decoder.PruneStale(maxAge)
		for _, v := range entry.decoder.GetActiveVessels(maxAge) {
			states = append(states, v.ToEntityState(seq))
		}
	}

	// Fall back to synthetic generator if no live data
	if len(states) == 0 && p.cfg.FallbackGenerator != nil {
		genStates := p.cfg.FallbackGenerator.Tick()
		states = append(states, genStates...)
	}

	return states
}
