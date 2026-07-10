// Package ais - TCP NMEA feed connector.
//
// Connects to an NMEA AIS TCP source (e.g. AISHub, gpsd, kplex),
// reads line-by-line, and pushes each sentence into a Decoder.
// Auto-reconnects on drop.
package ais

import (
	"bufio"
	"context"
	"log"
	"net"
	"time"
)

// FeedConfig configures an AIS NMEA TCP feed connector.
type FeedConfig struct {
	// Addr is the TCP address to connect to (e.g. "data.aishub.net:1234").
	Addr string
	// ReconnectDelay is the wait between reconnection attempts.
	// Defaults to 5 seconds if zero.
	ReconnectDelay time.Duration
}

// Feed connects to an NMEA TCP source and feeds lines into a Decoder.
type Feed struct {
	cfg     FeedConfig
	decoder *Decoder

	// stats
	linesTotal   uint64
	msgsTotal    uint64
	connectCount uint64
}

// NewFeed creates a new AIS NMEA feed connector attached to the given Decoder.
func NewFeed(cfg FeedConfig, dec *Decoder) *Feed {
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 5 * time.Second
	}
	return &Feed{
		cfg:     cfg,
		decoder: dec,
	}
}

// Connect starts the feed loop. It blocks until ctx is cancelled.
// On any read error it waits ReconnectDelay then reconnects.
func (f *Feed) Connect(ctx context.Context) error {
	statsTicker := time.NewTicker(30 * time.Second)
	defer statsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		f.connectCount++
		log.Printf("ais/feed: connecting to %s (attempt %d)", f.cfg.Addr, f.connectCount)

		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", f.cfg.Addr)
		if err != nil {
			log.Printf("ais/feed: dial %s failed: %v; retrying in %s", f.cfg.Addr, err, f.cfg.ReconnectDelay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(f.cfg.ReconnectDelay):
			}
			continue
		}

		log.Printf("ais/feed: connected to %s", f.cfg.Addr)
		f.runSession(ctx, conn, statsTicker)
		conn.Close()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.cfg.ReconnectDelay):
		}
	}
}

// runSession reads NMEA lines from conn until an error or ctx cancellation.
func (f *Feed) runSession(ctx context.Context, conn net.Conn, statsTicker *time.Ticker) {
	// Close the connection when ctx is cancelled so the scanner's Read returns.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		// Surface any pending stats
		select {
		case <-statsTicker.C:
			_, errs := f.decoder.Stats()
			log.Printf("ais/feed: addr=%s lines=%d msgs=%d decErrTotal=%d connCount=%d",
				f.cfg.Addr, f.linesTotal, f.msgsTotal, errs, f.connectCount)
		default:
		}

		line := scanner.Text()
		f.linesTotal++
		if f.decoder.FeedLine(line) {
			f.msgsTotal++
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			return
		default:
			log.Printf("ais/feed: scanner error from %s: %v", f.cfg.Addr, err)
		}
	}
}

// Stats returns cumulative lines read, messages decoded, and connect count.
func (f *Feed) Stats() (linesTotal, msgsTotal, connectCount uint64) {
	return f.linesTotal, f.msgsTotal, f.connectCount
}
