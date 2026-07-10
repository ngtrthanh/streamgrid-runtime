// Package adsb - TCP Beast feed connector.
//
// Connects to a Beast-format TCP source (e.g. dump1090 --net-beast-output),
// reads raw bytes, and pushes them into a Decoder. Auto-reconnects on drop.
package adsb

import (
	"context"
	"io"
	"log"
	"net"
	"time"
)

// FeedConfig configures a Beast TCP feed connector.
type FeedConfig struct {
	// Addr is the TCP address to connect to (e.g. "localhost:30005").
	Addr string
	// ReconnectDelay is the wait between reconnection attempts.
	// Defaults to 5 seconds if zero.
	ReconnectDelay time.Duration
}

// Feed connects to a Beast TCP source and feeds data into a Decoder.
type Feed struct {
	cfg     FeedConfig
	decoder *Decoder

	// stats
	bytesTotal   uint64
	msgsTotal    uint64
	connectCount uint64
}

// NewFeed creates a new Beast feed connector attached to the given Decoder.
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
		log.Printf("adsb/feed: connecting to %s (attempt %d)", f.cfg.Addr, f.connectCount)

		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", f.cfg.Addr)
		if err != nil {
			log.Printf("adsb/feed: dial %s failed: %v; retrying in %s", f.cfg.Addr, err, f.cfg.ReconnectDelay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(f.cfg.ReconnectDelay):
			}
			continue
		}

		log.Printf("adsb/feed: connected to %s", f.cfg.Addr)
		f.runSession(ctx, conn, statsTicker)
		conn.Close()

		// Don't reconnect if context was cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.cfg.ReconnectDelay):
		}
	}
}

// runSession reads from conn until an error or ctx cancellation.
func (f *Feed) runSession(ctx context.Context, conn net.Conn, statsTicker *time.Ticker) {
	buf := make([]byte, 4096)

	// Close the connection when ctx is cancelled so the blocking read returns.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		// Surface any pending stats
		select {
		case <-statsTicker.C:
			_, errs := f.decoder.Stats()
			log.Printf("adsb/feed: addr=%s bytes=%d msgs=%d decErrTotal=%d connCount=%d",
				f.cfg.Addr, f.bytesTotal, f.msgsTotal, errs, f.connectCount)
		default:
		}

		n, err := conn.Read(buf)
		if n > 0 {
			f.bytesTotal += uint64(n)
			decoded := f.decoder.Feed(buf[:n])
			f.msgsTotal += uint64(decoded)
		}
		if err != nil {
			if err != io.EOF {
				// Only log if it's not a deliberate shutdown
				select {
				case <-ctx.Done():
					return
				default:
					log.Printf("adsb/feed: read error from %s: %v", f.cfg.Addr, err)
				}
			}
			return
		}
	}
}

// Stats returns cumulative bytes read and messages decoded for this feed.
func (f *Feed) Stats() (bytesTotal, msgsTotal, connectCount uint64) {
	return f.bytesTotal, f.msgsTotal, f.connectCount
}
