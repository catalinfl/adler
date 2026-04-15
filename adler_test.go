package adler_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/catalinfl/adler"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func waitSignal(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(msg)
	}
}

func waitBytes(t *testing.T, ch <-chan []byte, msg string) []byte {
	t.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal(msg)
		return nil
	}
}

func waitSession(t *testing.T, ch <-chan *adler.Session, msg string) *adler.Session {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal(msg)
		return nil
	}
}

func readServerMessage(conn net.Conn, timeout time.Duration) (ws.OpCode, []byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, nil, err
	}
	defer conn.SetReadDeadline(time.Time{})

	payload, op, err := wsutil.ReadServerData(conn)
	if err != nil {
		return 0, nil, err
	}

	return op, payload, nil
}

func requireServerMessage(t *testing.T, conn net.Conn, timeout time.Duration, wantOp ws.OpCode, wantPayload []byte) {
	t.Helper()

	op, payload, err := readServerMessage(conn, timeout)
	if err != nil {
		t.Fatalf("read server message: %v", err)
	}

	if op != wantOp {
		t.Fatalf("unexpected opcode: got=%v want=%v", op, wantOp)
	}

	if string(payload) != string(wantPayload) {
		t.Fatalf("unexpected payload: got=%q want=%q", payload, wantPayload)
	}
}

func requireNoServerMessage(t *testing.T, conn net.Conn, timeout time.Duration) {
	t.Helper()

	_, _, err := readServerMessage(conn, timeout)
	if err == nil {
		t.Fatal("expected no message, but received one")
	}

	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return
	}

	t.Fatalf("expected timeout while waiting for no message, got: %v", err)
}

func mustDialWS(t *testing.T, url string) net.Conn {
	t.Helper()
	conn, _, _, err := ws.Dial(context.Background(), url)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return conn
}

func setupSingleSession(t *testing.T, a *adler.Adler) (net.Conn, *adler.Session) {
	t.Helper()

	sessions := make(chan *adler.Session, 1)
	a.HandleConnect(func(s *adler.Session) {
		sessions <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))
	t.Cleanup(srv.Close)

	conn := mustDialWS(t, wsURL(srv.URL))
	t.Cleanup(func() { _ = conn.Close() })

	return conn, waitSession(t, sessions, "session not captured")
}

func hasString(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

func hasAny(vals []any, want any) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

func TestHandleRequest(t *testing.T) {
	a := adler.New(adler.WithDispatchAsync(true))

	connected := make(chan struct{}, 1)
	disconnected := make(chan struct{}, 1)
	receivedText := make(chan []byte, 1)

	a.HandleConnect(func(s *adler.Session) {
		connected <- struct{}{}
	})

	a.HandleDisconnect(func(s *adler.Session) {
		disconnected <- struct{}{}
	})

	a.HandleMessage(func(s *adler.Session, msg []byte) {
		cp := append([]byte(nil), msg...)
		receivedText <- cp
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	},
	))

	defer srv.Close()

	conn, _, _, err := ws.Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}

	defer conn.Close()

	waitSignal(t, connected, "connect handler not called")

	want := []byte("hello")
	if err := wsutil.WriteClientMessage(conn, ws.OpText, want); err != nil {
		t.Fatalf("write client text: %v", err)
	}

	got := waitBytes(t, receivedText, "message handler not called")
	if string(got) != string(want) {
		t.Fatalf("unexpected message: got=%q want=%q", got, want)
	}

	_ = conn.Close()
	waitSignal(t, disconnected, "disconnect handler not called")
}

func TestRoomBroadcast(t *testing.T) {
	a := adler.New(adler.WithDispatchAsync(true))
	room := a.NewRoom("lobby")
	sessions := make(chan *adler.Session, 3)

	a.HandleConnect(func(s *adler.Session) {
		clientID := s.Request().URL.Query().Get("cid")
		s.Set("cid", clientID)
		s.SetIdentity(clientID)
		if err := room.Join(s); err != nil {
			t.Errorf("join room: %v", err)
			return
		}
		sessions <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))
	defer srv.Close()

	conns := map[string]net.Conn{}
	for _, cid := range []string{"1", "2", "3"} {
		conn, _, _, err := ws.Dial(context.Background(), wsURL(srv.URL)+"?cid="+cid)
		if err != nil {
			t.Fatalf("dial ws cid=%s: %v", cid, err)
		}
		conns[cid] = conn
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	captured := make([]*adler.Session, 0, 3)
	for range 3 {
		captured = append(captured, waitSession(t, sessions, "session connect not captured"))
	}

	if got := room.Len(); got != 3 {
		t.Fatalf("unexpected room size: got=%d want=3", got)
	}

	roomMsg := []byte("room-broadcast")
	room.Broadcast(roomMsg)
	for _, cid := range []string{"1", "2", "3"} {
		requireServerMessage(t, conns[cid], time.Second, ws.OpText, roomMsg)
	}

	var target *adler.Session
	for _, s := range captured {
		cid, ok := s.GetString("cid")
		if ok && cid == "2" {
			target = s
			break
		}
	}

	if target == nil {
		t.Fatal("target session for cid=2 not found")
	}

	privateMsg := []byte("private")
	if err := a.SendTo(privateMsg, target); err != nil {
		t.Fatalf("send to target: %v", err)
	}

	requireServerMessage(t, conns["2"], time.Second, ws.OpText, privateMsg)
	requireNoServerMessage(t, conns["1"], 150*time.Millisecond)
	requireNoServerMessage(t, conns["3"], 150*time.Millisecond)

	othersMsg := []byte("others")
	if err := a.BroadcastOthers(othersMsg, target); err != nil {
		t.Fatalf("broadcast others: %v", err)
	}

	requireServerMessage(t, conns["1"], time.Second, ws.OpText, othersMsg)
	requireServerMessage(t, conns["3"], time.Second, ws.OpText, othersMsg)
	requireNoServerMessage(t, conns["2"], 150*time.Millisecond)

	roomFilterMsg := []byte("room-filter")
	room.BroadcastFilter(roomFilterMsg, func(s *adler.Session) bool {
		cid, _ := s.GetString("cid")
		return cid != "2"
	})

	requireServerMessage(t, conns["1"], time.Second, ws.OpText, roomFilterMsg)
	requireServerMessage(t, conns["3"], time.Second, ws.OpText, roomFilterMsg)
	requireNoServerMessage(t, conns["2"], 150*time.Millisecond)

	room.Leave(target)
	if got := room.Len(); got != 2 {
		t.Fatalf("unexpected room size after leave: got=%d want=2", got)
	}
}

func TestSessionWrites(t *testing.T) {
	a := adler.New(adler.WithDispatchAsync(true))
	sessions := make(chan *adler.Session, 1)

	a.HandleConnect(func(s *adler.Session) {
		sessions <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))
	defer srv.Close()

	conn, _, _, err := ws.Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	s := waitSession(t, sessions, "session not captured")

	text := []byte("text-direct")
	if err := s.WriteText(text); err != nil {
		t.Fatalf("write text: %v", err)
	}
	requireServerMessage(t, conn, time.Second, ws.OpText, text)

	binary := []byte{1, 2, 3, 4}
	if err := s.WriteBinary(binary); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	requireServerMessage(t, conn, time.Second, ws.OpBinary, binary)

	jsonPayload := map[string]any{
		"kind": "session-json",
		"ok":   true,
	}

	if err := s.WriteJSON(jsonPayload); err != nil {
		t.Fatalf("write json: %v", err)
	}

	op, payload, err := readServerMessage(conn, time.Second)
	if err != nil {
		t.Fatalf("read session json: %v", err)
	}
	if op != ws.OpText {
		t.Fatalf("unexpected opcode for session json: got=%v want=%v", op, ws.OpText)
	}

	decodedSession := map[string]any{}
	if err := json.Unmarshal(payload, &decodedSession); err != nil {
		t.Fatalf("unmarshal session json: %v", err)
	}
	if decodedSession["kind"] != "session-json" {
		t.Fatalf("unexpected session json payload: %v", decodedSession)
	}

	broadcastJSON := map[string]any{"kind": "broadcast-json", "n": float64(7)}
	if err := a.BroadcastJSON(broadcastJSON); err != nil {
		t.Fatalf("broadcast json: %v", err)
	}

	op, payload, err = readServerMessage(conn, time.Second)
	if err != nil {
		t.Fatalf("read broadcast json: %v", err)
	}
	if op != ws.OpText {
		t.Fatalf("unexpected opcode for broadcast json: got=%v want=%v", op, ws.OpText)
	}

	decodedBroadcast := map[string]any{}
	if err := json.Unmarshal(payload, &decodedBroadcast); err != nil {
		t.Fatalf("unmarshal broadcast json: %v", err)
	}
	if decodedBroadcast["kind"] != "broadcast-json" {
		t.Fatalf("unexpected broadcast json payload: %v", decodedBroadcast)
	}
}

func TestSessionStore(t *testing.T) {
	a := adler.New(adler.WithDispatchAsync(true))
	conn, s := setupSingleSession(t, a)

	if s.LocalAddr() == nil {
		t.Fatal("local addr should not be nil")
	}
	if s.RemoteAddr() == nil {
		t.Fatal("remote addr should not be nil")
	}

	if s.Identity() {
		t.Fatal("identity should be false before SetIdentity")
	}
	s.SetIdentity("user-1")
	if !s.Identity() {
		t.Fatal("identity should be true after SetIdentity")
	}

	s.Set("name", "ana")
	if !s.Has("name") {
		t.Fatal("expected key name to exist")
	}

	v, err := s.Get("name")
	if err != nil {
		t.Fatalf("get existing key: %v", err)
	}
	if got, ok := v.(string); !ok || got != "ana" {
		t.Fatalf("unexpected get value: %#v", v)
	}

	if _, err := s.Get("missing"); err != adler.ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got: %v", err)
	}

	if !s.SetNX("once", 1) {
		t.Fatal("SetNX should return true for a new key")
	}
	if s.SetNX("once", 2) {
		t.Fatal("SetNX should return false when key already exists")
	}
	if iv, ok := s.GetInt("once"); !ok || iv != 1 {
		t.Fatalf("unexpected SetNX stored value: got=%d ok=%v", iv, ok)
	}

	s.Set("i", 7)
	s.Set("i64", int64(9))
	s.Set("f64", 3.14)
	s.Set("b", true)

	if got, ok := s.GetString("name"); !ok || got != "ana" {
		t.Fatalf("GetString failed: got=%q ok=%v", got, ok)
	}
	if got, ok := s.GetInt("i"); !ok || got != 7 {
		t.Fatalf("GetInt failed: got=%d ok=%v", got, ok)
	}
	if got, ok := s.GetInt64("i64"); !ok || got != 9 {
		t.Fatalf("GetInt64 failed: got=%d ok=%v", got, ok)
	}
	if got, ok := s.GetFloat("f64"); !ok || got != 3.14 {
		t.Fatalf("GetFloat failed: got=%f ok=%v", got, ok)
	}
	if got, ok := s.GetBool("b"); !ok || !got {
		t.Fatalf("GetBool failed: got=%v ok=%v", got, ok)
	}

	if _, ok := s.GetInt("name"); ok {
		t.Fatal("GetInt should fail for non-int value")
	}

	counter := int64(10)
	s.Set("counter", &counter)
	if got, err := s.Incr("counter"); err != nil || got != 11 {
		t.Fatalf("Incr failed: got=%d err=%v", got, err)
	}
	if got, err := s.Decr("counter"); err != nil || got != 10 {
		t.Fatalf("Decr failed: got=%d err=%v", got, err)
	}

	if _, err := s.Incr("missing-counter"); err != adler.ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound from Incr, got: %v", err)
	}

	s.Set("not-pointer", int64(5))
	if _, err := s.Decr("not-pointer"); err != adler.ErrTypeAssertionFailed {
		t.Fatalf("expected ErrTypeAssertionFailed from Decr, got: %v", err)
	}

	keys := s.Keys()
	if len(keys) == 0 || !hasString(keys, "name") || !hasString(keys, "counter") {
		t.Fatalf("unexpected keys snapshot: %v", keys)
	}

	values := s.Values()
	if len(values) == 0 || !hasAny(values, "ana") || !hasAny(values, true) {
		t.Fatalf("unexpected values snapshot: %v", values)
	}

	s.Unset("name")
	if s.Has("name") {
		t.Fatal("name should be removed after Unset")
	}

	s.Clear()
	if len(s.Keys()) != 0 || len(s.Values()) != 0 {
		t.Fatalf("expected empty store after Clear, got keys=%v values=%v", s.Keys(), s.Values())
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}
}

func TestRoomAndAdlerCallbacksAndRoomState(t *testing.T) {
	a := adler.New(adler.WithDispatchAsync(true))
	roomA := a.NewRoom("room-a")
	roomAAgain := a.NewRoom("room-a")
	if roomA != roomAAgain {
		t.Fatal("NewRoom should return same room instance for same name")
	}
	if roomA.Name() != "room-a" {
		t.Fatalf("unexpected room name: %s", roomA.Name())
	}

	if err := roomA.Join(nil); err != adler.ErrNilSession {
		t.Fatalf("expected ErrNilSession, got: %v", err)
	}

	roomA.CloseRoom()
	if err := roomA.Join(&adler.Session{}); err != adler.ErrRoomClosed {
		t.Fatalf("expected ErrRoomClosed, got: %v", err)
	}
	roomA.OpenRoom()

	sessions := make(chan *adler.Session, 2)
	roomJoin := make(chan struct{}, 2)
	roomLeave := make(chan struct{}, 2)
	adlerJoin := make(chan struct{}, 2)
	adlerLeave := make(chan struct{}, 2)

	roomA.HandleJoin(func(*adler.Session) { roomJoin <- struct{}{} })
	roomA.HandleLeave(func(*adler.Session) { roomLeave <- struct{}{} })
	a.OnRoomJoin(func(*adler.Session, *adler.Room) { adlerJoin <- struct{}{} })
	a.OnRoomLeave(func(*adler.Session, *adler.Room) { adlerLeave <- struct{}{} })

	a.HandleConnect(func(s *adler.Session) {
		if err := roomA.Join(s); err != nil {
			t.Errorf("join roomA: %v", err)
			return
		}
		sessions <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))
	defer srv.Close()

	c1 := mustDialWS(t, wsURL(srv.URL))
	defer c1.Close()
	c2 := mustDialWS(t, wsURL(srv.URL))
	defer c2.Close()

	s1 := waitSession(t, sessions, "missing session 1")
	_ = waitSession(t, sessions, "missing session 2")

	waitSignal(t, roomJoin, "room join callback not called")
	waitSignal(t, roomJoin, "room join callback not called second time")
	waitSignal(t, adlerJoin, "adler on room join callback not called")
	waitSignal(t, adlerJoin, "adler on room join callback not called second time")

	if s1.Room() != roomA {
		t.Fatal("session room should be roomA")
	}

	if got := roomA.Len(); got != 2 {
		t.Fatalf("unexpected room len: got=%d want=2", got)
	}
	if len(roomA.Sessions()) != 2 {
		t.Fatalf("unexpected Sessions snapshot len: %d", len(roomA.Sessions()))
	}

	roomA.Leave(s1)
	waitSignal(t, roomLeave, "room leave callback not called")
	waitSignal(t, adlerLeave, "adler on room leave callback not called")

	if s1.Room() != nil {
		t.Fatal("session room should be nil after leave")
	}
	if got := roomA.Len(); got != 1 {
		t.Fatalf("unexpected room len after leave: got=%d want=1", got)
	}
}

func TestAdlerBroadcastAndCloseState(t *testing.T) {
	a := adler.New(adler.WithDispatchAsync(true))
	sessions := make(chan *adler.Session, 2)

	a.HandleConnect(func(s *adler.Session) {
		id := s.Request().URL.Query().Get("id")
		s.Set("id", id)
		sessions <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))
	defer srv.Close()

	c1 := mustDialWS(t, wsURL(srv.URL)+"?id=1")
	defer c1.Close()
	c2 := mustDialWS(t, wsURL(srv.URL)+"?id=2")
	defer c2.Close()

	s1 := waitSession(t, sessions, "missing session 1")
	_ = waitSession(t, sessions, "missing session 2")

	if got := a.Len(); got != 2 {
		t.Fatalf("unexpected adler len: got=%d want=2", got)
	}
	if a.IsClosed() {
		t.Fatal("adler should not be closed yet")
	}

	msg := []byte("broadcast-all")
	if err := a.Broadcast(msg); err != nil {
		t.Fatalf("Broadcast failed: %v", err)
	}
	requireServerMessage(t, c1, time.Second, ws.OpText, msg)
	requireServerMessage(t, c2, time.Second, ws.OpText, msg)

	msgFiltered := []byte("broadcast-filter")
	if err := a.BroadcastFilter(msgFiltered, func(s *adler.Session) bool {
		id, _ := s.GetString("id")
		return id == "1"
	}); err != nil {
		t.Fatalf("BroadcastFilter failed: %v", err)
	}
	requireServerMessage(t, c1, time.Second, ws.OpText, msgFiltered)
	requireNoServerMessage(t, c2, 150*time.Millisecond)

	bin := []byte{9, 8, 7}
	if err := a.BroadcastBinary(bin); err != nil {
		t.Fatalf("BroadcastBinary failed: %v", err)
	}
	requireServerMessage(t, c1, time.Second, ws.OpBinary, bin)
	requireServerMessage(t, c2, time.Second, ws.OpBinary, bin)

	binFiltered := []byte{1, 3, 5}
	if err := a.BroadcastBinaryFilter(binFiltered, func(s *adler.Session) bool {
		id, _ := s.GetString("id")
		return id == "2"
	}); err != nil {
		t.Fatalf("BroadcastBinaryFilter failed: %v", err)
	}
	requireNoServerMessage(t, c1, 150*time.Millisecond)
	requireServerMessage(t, c2, time.Second, ws.OpBinary, binFiltered)

	if err := a.BroadcastJSONFilter(map[string]any{"k": "json-filter"}, func(s *adler.Session) bool {
		return s == s1
	}); err != nil {
		t.Fatalf("BroadcastJSONFilter failed: %v", err)
	}
	requireServerMessage(t, c1, time.Second, ws.OpText, []byte(`{"k":"json-filter"}`))
	requireNoServerMessage(t, c2, 150*time.Millisecond)

	if err := a.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if !a.IsClosed() {
		t.Fatal("adler should be closed after Close")
	}

	if err := a.Close(); err != adler.ErrCoreClosed {
		t.Fatalf("expected ErrCoreClosed on second Close, got: %v", err)
	}
	if err := a.Broadcast([]byte("x")); err != adler.ErrCoreClosed {
		t.Fatalf("expected ErrCoreClosed from Broadcast, got: %v", err)
	}
	if err := a.BroadcastBinary([]byte("x")); err != adler.ErrCoreClosed {
		t.Fatalf("expected ErrCoreClosed from BroadcastBinary, got: %v", err)
	}
	if err := a.SendTo([]byte("x"), s1); err != adler.ErrCoreClosed {
		t.Fatalf("expected ErrCoreClosed from SendTo, got: %v", err)
	}
	if err := a.BroadcastOthers([]byte("x"), s1); err != adler.ErrCoreClosed {
		t.Fatalf("expected ErrCoreClosed from BroadcastOthers, got: %v", err)
	}
}

func TestHandlersAndRoomBinaryJSONBroadcasts(t *testing.T) {
	a := adler.New(adler.WithDispatchAsync(false))
	room := a.NewRoom("room-handlers")
	sessions := make(chan *adler.Session, 2)

	binaryReceived := make(chan []byte, 1)
	pongReceived := make(chan struct{}, 1)
	sentText := make(chan []byte, 4)
	sentBinary := make(chan []byte, 4)

	a.HandleConnect(func(s *adler.Session) {
		s.Set("id", s.Request().URL.Query().Get("id"))
		if err := room.Join(s); err != nil {
			t.Errorf("join room: %v", err)
			return
		}
		sessions <- s
	})

	a.HandleMessageBinary(func(_ *adler.Session, msg []byte) {
		cp := append([]byte(nil), msg...)
		binaryReceived <- cp
	})
	a.HandlePong(func(*adler.Session) {
		pongReceived <- struct{}{}
	})
	a.HandleSentMessage(func(_ *adler.Session, msg []byte) {
		cp := append([]byte(nil), msg...)
		sentText <- cp
	})
	a.HandleSentMessageBinary(func(_ *adler.Session, msg []byte) {
		cp := append([]byte(nil), msg...)
		sentBinary <- cp
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))
	defer srv.Close()

	c1 := mustDialWS(t, wsURL(srv.URL)+"?id=1")
	defer c1.Close()
	c2 := mustDialWS(t, wsURL(srv.URL)+"?id=2")
	defer c2.Close()

	_ = waitSession(t, sessions, "missing session 1")
	_ = waitSession(t, sessions, "missing session 2")

	clientBinary := []byte("from-client-binary")
	if err := wsutil.WriteClientMessage(c1, ws.OpBinary, clientBinary); err != nil {
		t.Fatalf("write client binary: %v", err)
	}

	gotBinary := waitBytes(t, binaryReceived, "binary handler not called")
	if string(gotBinary) != string(clientBinary) {
		t.Fatalf("unexpected binary payload: got=%q want=%q", gotBinary, clientBinary)
	}

	if err := wsutil.WriteClientMessage(c1, ws.OpPong, []byte("pong")); err != nil {
		t.Fatalf("write pong: %v", err)
	}
	waitSignal(t, pongReceived, "pong handler not called")

	roomBinary := []byte{4, 2}
	room.BroadcastBinary(roomBinary)
	requireServerMessage(t, c1, time.Second, ws.OpBinary, roomBinary)
	requireServerMessage(t, c2, time.Second, ws.OpBinary, roomBinary)

	if string(waitBytes(t, sentBinary, "sent binary callback not called")) != string(roomBinary) {
		t.Fatal("unexpected sent binary payload")
	}

	if err := room.BroadcastJSON(map[string]any{"kind": "room-json"}); err != nil {
		t.Fatalf("room BroadcastJSON failed: %v", err)
	}
	requireServerMessage(t, c1, time.Second, ws.OpText, []byte(`{"kind":"room-json"}`))
	requireServerMessage(t, c2, time.Second, ws.OpText, []byte(`{"kind":"room-json"}`))

	if string(waitBytes(t, sentText, "sent text callback not called")) != `{"kind":"room-json"}` {
		t.Fatal("unexpected sent text payload")
	}

	if err := room.BroadcastJSONFilter(map[string]any{"kind": "room-json-filter"}, func(s *adler.Session) bool {
		id, _ := s.GetString("id")
		return id == "1"
	}); err != nil {
		t.Fatalf("room BroadcastJSONFilter failed: %v", err)
	}
	requireServerMessage(t, c1, time.Second, ws.OpText, []byte(`{"kind":"room-json-filter"}`))
	requireNoServerMessage(t, c2, 150*time.Millisecond)
}
