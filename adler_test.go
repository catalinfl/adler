package adler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
)

func TestAdlerHandleRequest_HubClosed(t *testing.T) {
	a := &Adler{hub: newHub()}
	a.hub.exit(message{})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	err := a.HandleRequest(rr, req)
	if !errors.Is(err, ErrHubClosed) {
		t.Fatalf("expected ErrHubClosed, got %v", err)
	}

	if got := a.hub.len(); got != 0 {
		t.Fatalf("expected no sessions in hub, got %d", got)
	}
}

func TestAdlerHandleRequest_UpgradeError(t *testing.T) {
	a := &Adler{hub: newHub()}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	err := a.HandleRequest(rr, req)
	if err == nil {
		t.Fatal("expected upgrade error, got nil")
	}

	if got := a.hub.len(); got != 0 {
		t.Fatalf("expected no sessions in hub after failed upgrade, got %d", got)
	}
}

func TestAdlerHandleRequest_SuccessRegistersSession(t *testing.T) {
	a := &Adler{hub: newHub()}
	done := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done <- a.HandleRequest(w, r)
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _, err := ws.Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("HandleRequest returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for HandleRequest to finish")
	}

	if got := a.hub.len(); got != 1 {
		t.Fatalf("expected one registered session, got %d", got)
	}

	a.hub.mu.RLock()
	defer a.hub.mu.RUnlock()

	for s := range a.hub.sessions {
		if s == nil {
			t.Fatal("expected non-nil session")
		}
		if s.Request == nil {
			t.Fatal("expected session request to be set")
		}
		if s.Conn == nil {
			t.Fatal("expected session connection to be set")
		}
		if s.Protocol != "" {
			t.Fatalf("expected empty protocol, got %q", s.Protocol)
		}
		if s.Keys == nil {
			t.Fatal("expected keys map to be initialized")
		}
		if s.Send == nil {
			t.Fatal("expected send channel to be initialized")
		}
		if cap(s.Send) != LargeBuffer {
			t.Fatalf("expected send channel capacity %d, got %d", LargeBuffer, cap(s.Send))
		}
	}
}
