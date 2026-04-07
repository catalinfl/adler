package adler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func newTestAdler(tb testing.TB) *Adler {
	tb.Helper()

	a := (&Adler{}).New()
	a.Config.PingPeriod = 24 * time.Hour
	a.Config.MessageBufferSize = 8

	// Set no-op handlers so tests only override the callbacks they care about.
	a.HandleConnect(func(*Session) {})
	a.HandleDisconnect(func(*Session) {})
	a.HandleMessage(func(*Session, []byte) {})
	a.HandleMessageBinary(func(*Session, []byte) {})
	a.HandleError(func(*Session, error) {})

	return a
}

func startWebSocketServer(tb testing.TB, a *Adler) string {
	tb.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.HandleRequest(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
	}))
	tb.Cleanup(server.Close)

	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func dialWebSocketClient(tb testing.TB, wsURL string) net.Conn {
	tb.Helper()

	conn, _, _, err := ws.Dial(context.Background(), wsURL)
	if err != nil {
		tb.Fatalf("dial websocket: %v", err)
	}

	tb.Cleanup(func() {
		_ = conn.Close()
	})

	return conn
}

func waitForSignal(tb testing.TB, signal <-chan struct{}, name string) {
	tb.Helper()

	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		tb.Fatalf("timeout waiting for %s", name)
	}
}

func readServerFrame(tb testing.TB, conn net.Conn) ([]byte, ws.OpCode) {
	tb.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		tb.Fatalf("set read deadline: %v", err)
	}

	payload, op, err := wsutil.ReadServerData(conn)
	if err != nil {
		tb.Fatalf("read server frame: %v", err)
	}

	return payload, op
}

func readServerFrameNoDeadline(tb testing.TB, conn net.Conn) ([]byte, ws.OpCode) {
	tb.Helper()

	payload, op, err := wsutil.ReadServerData(conn)
	if err != nil {
		tb.Fatalf("read server frame: %v", err)
	}

	return payload, op
}

func isExpectedDisconnectError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, net.ErrClosed) {
		return true
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "eof") ||
		strings.Contains(message, "closed network connection") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "forcibly closed")
}

func TestHandleRequest_TextRequestFlow(t *testing.T) {
	t.Parallel()

	a := newTestAdler(t)

	connected := make(chan struct{}, 1)
	disconnected := make(chan struct{}, 1)
	unexpectedErr := make(chan error, 1)

	a.HandleConnect(func(*Session) {
		connected <- struct{}{}
	})

	a.HandleDisconnect(func(*Session) {
		disconnected <- struct{}{}
	})

	a.HandleError(func(_ *Session, err error) {
		if isExpectedDisconnectError(err) {
			return
		}

		select {
		case unexpectedErr <- err:
		default:
		}
	})

	a.HandleMessage(func(s *Session, payload []byte) {
		var req struct {
			Action string `json:"action"`
			Name   string `json:"name"`
		}

		if err := json.Unmarshal(payload, &req); err != nil {
			_ = s.WriteJSON(map[string]string{
				"type":   "error",
				"reason": "invalid_json",
			})
			return
		}

		switch req.Action {
		case "hello":
			_ = s.WriteJSON(map[string]string{
				"type": "hello_ack",
				"name": req.Name,
			})
		default:
			_ = s.WriteJSON(map[string]string{
				"type":   "error",
				"reason": "unknown_action",
			})
		}
	})

	wsURL := startWebSocketServer(t, a)
	conn := dialWebSocketClient(t, wsURL)

	waitForSignal(t, connected, "connect handler")

	if err := wsutil.WriteClientText(conn, []byte(`{"action":"hello","name":"ana"}`)); err != nil {
		t.Fatalf("write client text: %v", err)
	}

	payload, op := readServerFrame(t, conn)
	if op != ws.OpText {
		t.Fatalf("unexpected websocket opcode: got %v want %v", op, ws.OpText)
	}

	var response map[string]string
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode json response: %v", err)
	}

	if response["type"] != "hello_ack" {
		t.Fatalf("unexpected response type: got %q", response["type"])
	}

	if response["name"] != "ana" {
		t.Fatalf("unexpected response name: got %q want %q", response["name"], "ana")
	}

	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close websocket client: %v", err)
	}

	waitForSignal(t, disconnected, "disconnect handler")

	select {
	case err := <-unexpectedErr:
		t.Fatalf("unexpected websocket error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHandleRequest_BinaryRequestFlow(t *testing.T) {
	t.Parallel()

	a := newTestAdler(t)

	connected := make(chan struct{}, 1)
	disconnected := make(chan struct{}, 1)
	seenBinary := make(chan []byte, 1)
	unexpectedErr := make(chan error, 1)

	a.HandleConnect(func(*Session) {
		connected <- struct{}{}
	})

	a.HandleDisconnect(func(*Session) {
		disconnected <- struct{}{}
	})

	a.HandleError(func(_ *Session, err error) {
		if isExpectedDisconnectError(err) {
			return
		}

		select {
		case unexpectedErr <- err:
		default:
		}
	})

	a.HandleMessageBinary(func(s *Session, payload []byte) {
		copyOfPayload := append([]byte(nil), payload...)

		select {
		case seenBinary <- copyOfPayload:
		default:
		}

		_ = s.WriteBinary(copyOfPayload)
	})

	wsURL := startWebSocketServer(t, a)
	conn := dialWebSocketClient(t, wsURL)

	waitForSignal(t, connected, "connect handler")

	want := []byte{0x10, 0x20, 0x30}
	if err := wsutil.WriteClientBinary(conn, want); err != nil {
		t.Fatalf("write client binary: %v", err)
	}

	select {
	case got := <-seenBinary:
		if !bytes.Equal(got, want) {
			t.Fatalf("binary handler payload mismatch: got %v want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for binary handler")
	}

	payload, op := readServerFrame(t, conn)
	if op != ws.OpBinary {
		t.Fatalf("unexpected websocket opcode: got %v want %v", op, ws.OpBinary)
	}

	if !bytes.Equal(payload, want) {
		t.Fatalf("binary echo mismatch: got %v want %v", payload, want)
	}

	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close websocket client: %v", err)
	}

	waitForSignal(t, disconnected, "disconnect handler")

	select {
	case err := <-unexpectedErr:
		t.Fatalf("unexpected websocket error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func BenchmarkHandleRequest_TwoUsersChatFlow(b *testing.B) {
	b.ReportAllocs()

	a := newTestAdler(b)
	a.Config.DispatchAsync = false

	connected := make(chan struct{}, 2)
	disconnected := make(chan struct{}, 2)
	unexpectedErr := make(chan error, 64)

	var usersMu sync.RWMutex
	usersByName := make(map[string]*Session)
	nameBySession := make(map[*Session]string)

	joinAnaReq := []byte("join:ana")
	joinMariaReq := []byte("join:maria")
	joinAnaAck := []byte("join_ack:ana")
	joinMariaAck := []byte("join_ack:maria")
	chatAnaToMariaReq := []byte("chat:ana:maria:ping")
	chatMariaToAnaReq := []byte("chat:maria:ana:pong")
	chatAnaToMariaResp := []byte("chat:ana:ping")
	chatMariaToAnaResp := []byte("chat:maria:pong")
	recipientNotFound := []byte("error:recipient_not_found")
	unknownReq := []byte("error:unknown_request")

	a.HandleConnect(func(*Session) {
		select {
		case connected <- struct{}{}:
		default:
		}
	})

	a.HandleDisconnect(func(s *Session) {
		usersMu.Lock()
		if name, ok := nameBySession[s]; ok {
			delete(nameBySession, s)
			delete(usersByName, name)
		}
		usersMu.Unlock()

		select {
		case disconnected <- struct{}{}:
		default:
		}
	})

	a.HandleError(func(_ *Session, err error) {
		if isExpectedDisconnectError(err) {
			return
		}

		select {
		case unexpectedErr <- err:
		default:
		}
	})

	a.HandleMessage(func(sender *Session, payload []byte) {
		switch {
		case bytes.Equal(payload, joinAnaReq):
			usersMu.Lock()
			usersByName["ana"] = sender
			nameBySession[sender] = "ana"
			usersMu.Unlock()
			_ = sender.WriteText(joinAnaAck)
		case bytes.Equal(payload, joinMariaReq):
			usersMu.Lock()
			usersByName["maria"] = sender
			nameBySession[sender] = "maria"
			usersMu.Unlock()
			_ = sender.WriteText(joinMariaAck)
		case bytes.Equal(payload, chatAnaToMariaReq):
			usersMu.RLock()
			recipient := usersByName["maria"]
			usersMu.RUnlock()

			if recipient == nil {
				_ = sender.WriteText(recipientNotFound)
				return
			}
			_ = recipient.WriteText(chatAnaToMariaResp)
		case bytes.Equal(payload, chatMariaToAnaReq):
			usersMu.RLock()
			recipient := usersByName["ana"]
			usersMu.RUnlock()

			if recipient == nil {
				_ = sender.WriteText(recipientNotFound)
				return
			}
			_ = recipient.WriteText(chatMariaToAnaResp)
		default:
			_ = sender.WriteText(unknownReq)
		}
	})

	wsURL := startWebSocketServer(b, a)
	userOne := dialWebSocketClient(b, wsURL)
	userTwo := dialWebSocketClient(b, wsURL)

	waitForSignal(b, connected, "first user connect")
	waitForSignal(b, connected, "second user connect")

	if err := wsutil.WriteClientText(userOne, joinAnaReq); err != nil {
		b.Fatalf("user one join: %v", err)
	}

	joinOnePayload, joinOneOp := readServerFrameNoDeadline(b, userOne)
	if joinOneOp != ws.OpText {
		b.Fatalf("unexpected join ack opcode for user one: got %v want %v", joinOneOp, ws.OpText)
	}

	if !bytes.Equal(joinOnePayload, joinAnaAck) {
		b.Fatalf("unexpected join ack for user one: got %q want %q", joinOnePayload, joinAnaAck)
	}

	if err := wsutil.WriteClientText(userTwo, joinMariaReq); err != nil {
		b.Fatalf("user two join: %v", err)
	}

	joinTwoPayload, joinTwoOp := readServerFrameNoDeadline(b, userTwo)
	if joinTwoOp != ws.OpText {
		b.Fatalf("unexpected join ack opcode for user two: got %v want %v", joinTwoOp, ws.OpText)
	}

	if !bytes.Equal(joinTwoPayload, joinMariaAck) {
		b.Fatalf("unexpected join ack for user two: got %q want %q", joinTwoPayload, joinMariaAck)
	}

	if err := userOne.SetReadDeadline(time.Now().Add(10 * time.Minute)); err != nil {
		b.Fatalf("set user one read deadline: %v", err)
	}

	if err := userTwo.SetReadDeadline(time.Now().Add(10 * time.Minute)); err != nil {
		b.Fatalf("set user two read deadline: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := wsutil.WriteClientText(userOne, chatAnaToMariaReq); err != nil {
			b.Fatalf("write user one chat: %v", err)
		}

		msgForTwoPayload, msgForTwoOp := readServerFrameNoDeadline(b, userTwo)
		if msgForTwoOp != ws.OpText {
			b.Fatalf("unexpected opcode for user two message: got %v want %v", msgForTwoOp, ws.OpText)
		}

		if !bytes.Equal(msgForTwoPayload, chatAnaToMariaResp) {
			b.Fatalf("unexpected payload for user two: got %q want %q", msgForTwoPayload, chatAnaToMariaResp)
		}

		if err := wsutil.WriteClientText(userTwo, chatMariaToAnaReq); err != nil {
			b.Fatalf("write user two chat: %v", err)
		}

		msgForOnePayload, msgForOneOp := readServerFrameNoDeadline(b, userOne)
		if msgForOneOp != ws.OpText {
			b.Fatalf("unexpected opcode for user one message: got %v want %v", msgForOneOp, ws.OpText)
		}

		if !bytes.Equal(msgForOnePayload, chatMariaToAnaResp) {
			b.Fatalf("unexpected payload for user one: got %q want %q", msgForOnePayload, chatMariaToAnaResp)
		}
	}
	b.StopTimer()

	if err := userOne.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		b.Fatalf("close user one connection: %v", err)
	}

	if err := userTwo.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		b.Fatalf("close user two connection: %v", err)
	}

	waitForSignal(b, disconnected, "first user disconnect")
	waitForSignal(b, disconnected, "second user disconnect")

	select {
	case err := <-unexpectedErr:
		b.Fatalf("unexpected websocket error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHandleRequest_ReturnsErrHubClosed(t *testing.T) {
	t.Parallel()

	a := newTestAdler(t)
	a.core.closed.Store(true)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	res := httptest.NewRecorder()

	err := a.HandleRequest(res, req)
	if !errors.Is(err, ErrCoreClosed) {
		t.Fatalf("unexpected error: got %v want %v", err, ErrCoreClosed)
	}
}

// BenchmarkLargePayload_Adler
func BenchmarkLargePayload_Adler(b *testing.B) {
	b.ReportAllocs()

	payload := make([]byte, 64*1024) // 64 kB
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	a := newTestAdler(b)
	a.Config.DispatchAsync = false

	received := make(chan struct{}, 1)

	a.HandleMessage(func(s *Session, msg []byte) {
		_ = s.WriteText(msg)
	})

	a.HandleConnect(func(*Session) { received <- struct{}{} })

	wsURL := startWebSocketServer(b, a)
	conn := dialWebSocketClient(b, wsURL)
	waitForSignal(b, received, "connect")

	reader := wsutil.NewReader(conn, ws.StateClientSide)

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := wsutil.WriteClientText(conn, payload); err != nil {
			b.Fatal(err)
		}

		hdr, err := reader.NextFrame()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, reader); err != nil {
			b.Fatal(err)
		}
		_ = hdr
	}

	b.StopTimer()
}
