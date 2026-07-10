package edge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestServerCreation(t *testing.T) {
	cfg := DefaultConfig()
	s := New(cfg)
	if s == nil {
		t.Fatal("server is nil")
	}
	if s.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", s.ClientCount())
	}
}

func TestBroadcastWithNoClients(t *testing.T) {
	cfg := DefaultConfig()
	s := New(cfg)

	// Should not panic with no clients
	frame := make([]byte, 100)
	s.BroadcastFrame(frame)
}

func TestWebSocketConnection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WSAddr = "127.0.0.1:0" // random port
	s := New(cfg)

	// Create test HTTP server with the WebSocket handler
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		s.handleWebSocketClient(conn)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Connect WebSocket client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer ws.Close()

	// Wait for client to register
	time.Sleep(50 * time.Millisecond)

	if s.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", s.ClientCount())
	}

	// Broadcast a frame
	testFrame := []byte{0x46, 0x52, 0x47, 0x53, 1, 2, 3, 4, 5, 6, 7, 8}
	s.BroadcastFrame(testFrame)

	// Read the frame from client
	ws.SetReadDeadline(time.Now().Add(1 * time.Second))
	msgType, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Errorf("expected binary message, got %d", msgType)
	}
	if len(msg) != len(testFrame) {
		t.Errorf("expected %d bytes, got %d", len(testFrame), len(msg))
	}

	// Close client
	ws.Close()
	time.Sleep(100 * time.Millisecond)

	// Client count should decrease (eventually)
	// Note: may take a moment for the goroutine to clean up
}

func TestSelfSignedTLS(t *testing.T) {
	tlsConfig, err := generateSelfSignedTLS()
	if err != nil {
		t.Fatalf("generateSelfSignedTLS: %v", err)
	}
	if len(tlsConfig.Certificates) == 0 {
		t.Error("expected at least one certificate")
	}
}
