package matchmaking_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/catalinfl/adler"
	matchmaking "github.com/catalinfl/adler/matchmaker"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type queueEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	RoomID  string `json:"room_id"`
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
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

func readServerMessage(t *testing.T, conn net.Conn, timeout time.Duration) (ws.OpCode, []byte) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	payload, op, err := wsutil.ReadServerData(conn)
	if err != nil {
		t.Fatalf("read server message: %v", err)
	}

	return op, payload
}

func mustDialWS(t *testing.T, url string) net.Conn {
	t.Helper()

	conn, _, _, err := ws.Dial(context.Background(), url)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return conn
}

func waitQueueEvent(t *testing.T, conn net.Conn) queueEvent {
	t.Helper()

	op, payload := readServerMessage(t, conn, 2*time.Second)
	if op != ws.OpText {
		t.Fatalf("unexpected opcode: got=%v want=%v", op, ws.OpText)
	}

	var event queueEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal server event: %v", err)
	}

	return event
}

func waitMatchRoomID(t *testing.T, conn net.Conn) string {
	t.Helper()

	first := waitQueueEvent(t, conn)
	if first.Type != "queue_joined" {
		t.Fatalf("unexpected first event: %#v", first)
	}

	second := waitQueueEvent(t, conn)
	if second.Type != "match_found" {
		t.Fatalf("unexpected second event: %#v", second)
	}
	if second.RoomID == "" {
		t.Fatal("match_found event did not include a room_id")
	}

	return second.RoomID
}

func setupMatchmakerServer(t *testing.T, roomSize int) (*adler.Adler, *httptest.Server, chan *adler.Session) {
	t.Helper()

	a := adler.New(
		adler.WithDispatchAsync(true),
		adler.WithMessageBufferSize(8),
	)
	mm := matchmaking.NewMatchmaker(a, matchmaking.WithRoomSize(roomSize))
	sessions := make(chan *adler.Session, 8)

	a.HandleConnect(func(s *adler.Session) {
		mm.AddToQueue(s)
		sessions <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))

	t.Cleanup(srv.Close)

	return a, srv, sessions
}

func TestMatchmakerCreatesRoomWithRealConnections(t *testing.T) {
	a, srv, sessions := setupMatchmakerServer(t, 2)

	conn1 := mustDialWS(t, wsURL(srv.URL)+"?cid=1")
	defer conn1.Close()

	conn2 := mustDialWS(t, wsURL(srv.URL)+"?cid=2")
	defer conn2.Close()

	s1 := waitSession(t, sessions, "missing first session")
	s2 := waitSession(t, sessions, "missing second session")

	roomID1 := waitMatchRoomID(t, conn1)
	roomID2 := waitMatchRoomID(t, conn2)

	if roomID1 != roomID2 {
		t.Fatalf("players were matched into different rooms: %q vs %q", roomID1, roomID2)
	}

	room, err := a.Room(roomID1)
	if err != nil {
		t.Fatalf("room lookup failed: %v", err)
	}

	if got := room.Len(); got != 2 {
		t.Fatalf("unexpected room size: got=%d want=2", got)
	}

	if got := s1.Room(); got != room {
		t.Fatalf("first session room mismatch: got=%p want=%p", got, room)
	}
	if got := s2.Room(); got != room {
		t.Fatalf("second session room mismatch: got=%p want=%p", got, room)
	}

	if got := room.Name(); got != roomID1 {
		t.Fatalf("unexpected room name: got=%q want=%q", got, roomID1)
	}
}

func TestMatchmakerCreatesSeparateRoomsForMultiplePairs(t *testing.T) {
	a, srv, sessions := setupMatchmakerServer(t, 2)

	conns := make([]net.Conn, 0, 4)
	for i := 1; i <= 4; i++ {
		conn := mustDialWS(t, wsURL(srv.URL)+"?cid="+fmt.Sprintf("%d", i))
		conns = append(conns, conn)
		defer conn.Close()
	}

	captured := make([]*adler.Session, 0, 4)
	for range 4 {
		captured = append(captured, waitSession(t, sessions, "missing connected session"))
	}

	roomIDs := make([]string, 0, 4)
	for _, conn := range conns {
		roomIDs = append(roomIDs, waitMatchRoomID(t, conn))
	}

	if roomIDs[0] != roomIDs[1] {
		t.Fatalf("first pair matched into different rooms: %q vs %q", roomIDs[0], roomIDs[1])
	}
	if roomIDs[2] != roomIDs[3] {
		t.Fatalf("second pair matched into different rooms: %q vs %q", roomIDs[2], roomIDs[3])
	}
	if roomIDs[0] == roomIDs[2] {
		t.Fatalf("expected two separate rooms, both pairs got %q", roomIDs[0])
	}

	room1, err := a.Room(roomIDs[0])
	if err != nil {
		t.Fatalf("room 1 lookup failed: %v", err)
	}
	room2, err := a.Room(roomIDs[2])
	if err != nil {
		t.Fatalf("room 2 lookup failed: %v", err)
	}

	if got := room1.Len(); got != 2 {
		t.Fatalf("unexpected first room size: got=%d want=2", got)
	}
	if got := room2.Len(); got != 2 {
		t.Fatalf("unexpected second room size: got=%d want=2", got)
	}

	if got := captured[0].Room(); got == nil || got.Name() != roomIDs[0] {
		t.Fatalf("first captured session is not in the expected room: got=%v room=%q", got, roomIDs[0])
	}
	if got := captured[2].Room(); got == nil || got.Name() != roomIDs[2] {
		t.Fatalf("third captured session is not in the expected room: got=%v room=%q", got, roomIDs[2])
	}
}

func TestClosureBugPromotedPlayers(t *testing.T) {
	a := adler.New(
		adler.WithDispatchAsync(false), // Sync mode pentru debugging
		adler.WithMessageBufferSize(32),
	)
	mm := matchmaking.NewMatchmaker(
		a,
		matchmaking.WithRoomSize(10),
		matchmaking.WithQueueLength(2),
	)

	sessions := make(chan *adler.Session, 10)
	a.HandleConnect(func(s *adler.Session) {
		cid := s.Request().URL.Query().Get("cid")
		s.Set("cid", cid)
		mm.AddToQueue(s)
		sessions <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.HandleRequest(w, r)
	}))
	defer srv.Close()

	// Conectez 5 playeri
	conns := make([]net.Conn, 0, 5)
	for i := 1; i <= 5; i++ {
		conn := mustDialWS(t, wsURL(srv.URL)+"?cid="+fmt.Sprintf("%d", i))
		conns = append(conns, conn)
		defer conn.Close()
	}

	// Consume initial queue notifications so we can assert only promotion events later.
	for i := 0; i < 5; i++ {
		evt := waitQueueEvent(t, conns[i])
		if i < 2 {
			if evt.Type != "queue_joined" {
				t.Fatalf("player %d expected queue_joined, got %q", i+1, evt.Type)
			}
			continue
		}

		if evt.Type != "wait_queue_joined" {
			t.Fatalf("player %d expected wait_queue_joined, got %q", i+1, evt.Type)
		}
	}

	captured := make([]*adler.Session, 0, 5)
	for i := 0; i < 5; i++ {
		s := waitSession(t, sessions, fmt.Sprintf("missing session %d", i+1))
		captured = append(captured, s)
		status, _ := s.GetString("queue_status")
		t.Logf("Player %d status: %s", i+1, status)
	}

	// Apel RemoveFromQueue pe p1
	t.Log("Calling RemoveFromQueue(p1)...")
	mm.RemoveFromQueue(captured[0])
	time.Sleep(100 * time.Millisecond) // Give time for notifications

	// Removing one main-queue player opens exactly one slot, so only p3 is promoted.
	t.Log("Waiting for promoted_to_queue on player 3 (conns[2])...")
	op, payload := readServerMessage(t, conns[2], 500*time.Millisecond)
	t.Logf("Player 3 received: opcode=%v, payload=%s", op, string(payload))

	var evt queueEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		t.Fatalf("player 3 unmarshal error: %v", err)
	}
	if evt.Type != "promoted_to_queue" {
		t.Fatalf("player 3 expected promoted_to_queue, got %q", evt.Type)
	}

	t.Log("Promotion flow works: waiting player receives promoted_to_queue on correct connection")
}
