// Package edge implements the StreamGrid edge server.
//
// The edge server accepts connections from browsers via WebTransport (HTTP/3)
// or WebSocket (fallback), and streams binary entity state frames from the
// generator or backend.
package edge

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

// Server is the StreamGrid edge server.
type Server struct {
	config     Config
	clients    sync.Map // map[uint64]*Client
	nextID     atomic.Uint64
	clientCount atomic.Int64

	// Frame broadcaster
	frameCh chan []byte

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// Config configures the edge server.
type Config struct {
	// WebTransport listen address (UDP + TCP for HTTP/3)
	WTAddr string
	// WebSocket listen address
	WSAddr string
	// TLS certificate and key (auto-generated if empty)
	CertFile string
	KeyFile  string
	// Maximum frame buffer per client before dropping
	MaxBufferFrames int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		WTAddr:          ":4433",
		WSAddr:          ":8080",
		MaxBufferFrames: 10,
	}
}

// Client represents a connected browser client.
type Client struct {
	ID       uint64
	Protocol string // "webtransport" or "websocket"
	frameCh  chan []byte
	done     chan struct{}
}

// New creates a new edge server.
func New(cfg Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		config:  cfg,
		frameCh: make(chan []byte, 100),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// BroadcastFrame sends a frame to all connected clients.
func (s *Server) BroadcastFrame(frame []byte) {
	// Make a copy since we're sending to multiple clients
	frameCopy := make([]byte, len(frame))
	copy(frameCopy, frame)

	s.clients.Range(func(key, value interface{}) bool {
		client := value.(*Client)
		select {
		case client.frameCh <- frameCopy:
		default:
			// Client buffer full — drop frame (acceptable for real-time streaming)
		}
		return true
	})
}

// ClientCount returns the number of connected clients.
func (s *Server) ClientCount() int64 {
	return s.clientCount.Load()
}

// Start starts both WebTransport and WebSocket servers.
func (s *Server) Start() error {
	tlsConfig, err := s.getTLSConfig()
	if err != nil {
		return fmt.Errorf("TLS config: %w", err)
	}

	errCh := make(chan error, 2)

	// Start WebSocket server
	go func() {
		errCh <- s.startWebSocket()
	}()

	// Start WebTransport server
	go func() {
		errCh <- s.startWebTransport(tlsConfig)
	}()

	// Return first error (or block until shutdown)
	select {
	case err := <-errCh:
		return err
	case <-s.ctx.Done():
		return nil
	}
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	s.cancel()
}

func (s *Server) startWebSocket() error {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket upgrade error: %v", err)
			return
		}
		s.handleWebSocketClient(conn)
	})

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"status":"ok","clients":%d}`, s.ClientCount())
	})

	server := &http.Server{
		Addr:    s.config.WSAddr,
		Handler: mux,
	}

	log.Printf("WebSocket server listening on %s", s.config.WSAddr)

	go func() {
		<-s.ctx.Done()
		server.Close()
	}()

	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleWebSocketClient(conn *websocket.Conn) {
	id := s.nextID.Add(1)
	client := &Client{
		ID:       id,
		Protocol: "websocket",
		frameCh:  make(chan []byte, s.config.MaxBufferFrames),
		done:     make(chan struct{}),
	}

	s.clients.Store(id, client)
	s.clientCount.Add(1)
	log.Printf("WebSocket client connected: #%d (total: %d)", id, s.ClientCount())

	defer func() {
		s.clients.Delete(id)
		s.clientCount.Add(-1)
		conn.Close()
		close(client.done)
		log.Printf("WebSocket client disconnected: #%d (total: %d)", id, s.ClientCount())
	}()

	// Read pump (handles pings/close)
	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				close(client.frameCh)
				return
			}
		}
	}()

	// Write pump
	for frame := range client.frameCh {
		conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		err := conn.WriteMessage(websocket.BinaryMessage, frame)
		if err != nil {
			return
		}
	}
}

func (s *Server) startWebTransport(tlsConfig *tls.Config) error {
	wtServer := &webtransport.Server{
		H3: &http3.Server{
			Addr:      s.config.WTAddr,
			TLSConfig: tlsConfig,
		},
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	http.HandleFunc("/wt", func(w http.ResponseWriter, r *http.Request) {
		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			log.Printf("WebTransport upgrade error: %v", err)
			return
		}
		s.handleWebTransportSession(session)
	})

	log.Printf("WebTransport server listening on %s", s.config.WTAddr)

	go func() {
		<-s.ctx.Done()
		wtServer.Close()
	}()

	err := wtServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleWebTransportSession(session *webtransport.Session) {
	id := s.nextID.Add(1)
	client := &Client{
		ID:       id,
		Protocol: "webtransport",
		frameCh:  make(chan []byte, s.config.MaxBufferFrames),
		done:     make(chan struct{}),
	}

	s.clients.Store(id, client)
	s.clientCount.Add(1)
	log.Printf("WebTransport client connected: #%d (total: %d)", id, s.ClientCount())

	defer func() {
		s.clients.Delete(id)
		s.clientCount.Add(-1)
		session.CloseWithError(0, "server closing")
		close(client.done)
		log.Printf("WebTransport client disconnected: #%d (total: %d)", id, s.ClientCount())
	}()

	// Open a unidirectional stream for frame delivery
	stream, err := session.OpenUniStream()
	if err != nil {
		log.Printf("WebTransport open stream error: %v", err)
		return
	}

	// Write frames to the stream
	for frame := range client.frameCh {
		// Write frame length prefix (4 bytes) + frame data
		lenBuf := []byte{
			byte(len(frame)),
			byte(len(frame) >> 8),
			byte(len(frame) >> 16),
			byte(len(frame) >> 24),
		}
		if _, err := stream.Write(lenBuf); err != nil {
			return
		}
		if _, err := stream.Write(frame); err != nil {
			return
		}
	}
}

func (s *Server) getTLSConfig() (*tls.Config, error) {
	if s.config.CertFile != "" && s.config.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.config.CertFile, s.config.KeyFile)
		if err != nil {
			return nil, err
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
		}, nil
	}

	// Generate self-signed certificate for development
	return generateSelfSignedTLS()
}

func generateSelfSignedTLS() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"StreamGrid Dev"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil
}
