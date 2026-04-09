package adler

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type messageFunc func(*Session, []byte)
type errorFunc func(*Session, error)
type closeFunc func(*Session, int, string) error
type sessionFunc func(*Session)
type filterFunc func(*Session) bool
type roomSessionFunc func(*Session, *Room)

type handlers struct {
	messageHandler           messageFunc
	messageHandlerBinary     messageFunc
	messageSentHandler       messageFunc
	messageSentHandlerBinary messageFunc
	errorHandler             errorFunc
	closeHandler             closeFunc
	connectHandler           sessionFunc
	disconnectHandler        sessionFunc
	pongHandler              sessionFunc
	onRoomLeave              roomSessionFunc
	onRoomJoin               roomSessionFunc
}

const LargeBuffer int = 1024
const MediumBuffer int = 512
const SmallBuffer int = 256

type Adler struct {
	Config   *Config
	core     *core
	handlers handlers
	rooms    map[string]*Room
	roomsMu  sync.RWMutex
}

func (a *Adler) New() *Adler {
	// no handler functions are initialized by default, all are nil
	handlers := handlers{}

	return &Adler{
		Config:   newConfig(),
		core:     newCore(),
		handlers: handlers,
		rooms:    make(map[string]*Room),
	}
}

func (a *Adler) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	if a.core.isClosed() {
		return ErrCoreClosed
	}

	outputBufferSize := 1
	if a.Config != nil && a.Config.MessageBufferSize > 0 {
		outputBufferSize = int(a.Config.MessageBufferSize)
	}

	conn, err := a.upgradeWebSocket(r, w)
	if err != nil {
		return err
	}

	session := &Session{
		Store:      make(map[string]any),
		Request:    r,
		Protocol:   r.Proto,
		Conn:       conn,
		output:     make(chan message, outputBufferSize),
		outputDone: make(chan struct{}),
		mu:         sync.RWMutex{},
		closed:     false,
		adler:      a,
	}

	// must have a reader to reduce number of allocations per session
	session.reader = wsutil.NewReader(conn, ws.StateServerSide)

	// session.readBuf.Grow(64 * 1024)

	if err := a.core.register(session); err != nil {
		_ = conn.Close()
		return err
	}

	if a.handlers.connectHandler != nil {
		a.handlers.connectHandler(session)
	}
	go session.writePump()
	session.readPump()

	if a.core.isClosed() {
		a.core.unregister(session)
	}

	session.mu.Lock()
	room := session.room
	session.room = nil
	session.mu.Unlock()

	if room != nil {
		room.mu.Lock()
		delete(room.sessions, session)
		room.mu.Unlock()

		if room.handlers.onLeave != nil {
			room.handlers.onLeave(session)
		}

		if a.handlers.onRoomLeave != nil {
			a.handlers.onRoomLeave(session, room)
		}
	}

	if a.handlers.disconnectHandler != nil {
		a.handlers.disconnectHandler(session)
	}

	session.close()
	return nil
}

func (a *Adler) broadcast(msg []byte, messageType ws.OpCode, fn ...func(*Session) bool) error {
	if a.core.isClosed() {
		return ErrCoreClosed
	}

	var filter func(*Session) bool

	if len(fn) > 0 {
		filter = fn[0]
	}

	a.core.broadcast(message{
		messageType: messageType,
		content:     msg,
		filter:      filter,
	})

	return nil
}

func (a *Adler) Broadcast(msg []byte) error {
	return a.broadcast(msg, ws.OpText)
}

func (a *Adler) BroadcastFilter(msg []byte, fn func(*Session) bool) error {
	return a.broadcast(msg, ws.OpText, fn)
}

func (a *Adler) BroadcastBinary(msg []byte) error {
	return a.broadcast(msg, ws.OpBinary)
}

func (a *Adler) BroadcastBinaryFilter(msg []byte, fn func(*Session) bool) error {
	return a.broadcast(msg, ws.OpBinary, fn)
}

func (a *Adler) BroadcastJSON(v any) error {
	content, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return a.Broadcast(content)
}

func (a *Adler) BroadcastJSONFilter(v any, fn func(*Session) bool) error {
	content, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return a.BroadcastFilter(content, fn)
}

func (a *Adler) Len() int {
	return a.core.len()
}

func (a *Adler) IsClosed() bool {
	return a.core.isClosed()
}

func (a *Adler) Close() error {
	if a.core.isClosed() {
		return ErrCoreClosed
	}

	a.core.exit(message{
		messageType: ws.OpClose,
		content:     []byte(""),
	})
	return nil
}

func (a *Adler) NewRoom(name string) *Room {
	a.roomsMu.Lock()
	defer a.roomsMu.Unlock()

	if a.rooms == nil {
		a.rooms = make(map[string]*Room)
	}

	if r, ok := a.rooms[name]; ok {
		return r
	}

	r := newRoom(name, a)
	a.rooms[name] = r
	return r
}

func (a *Adler) removeRoomIfEmpty(r *Room) {
	if r == nil || r.Len() != 0 {
		return
	}

	a.roomsMu.Lock()
	defer a.roomsMu.Unlock()

	if a.rooms == nil {
		return
	}

	current, ok := a.rooms[r.name]
	if !ok || current != r {
		return
	}

	if r.Len() == 0 {
		delete(a.rooms, r.name)
	}
}

func (a *Adler) BroadcastOthers(msg []byte, s *Session) error {
	return a.BroadcastFilter(msg, func(other *Session) bool {
		return other != s
	})
}

func (a *Adler) SendTo(msg []byte, s *Session) error {
	return a.BroadcastFilter(msg, func(other *Session) bool {
		return other == s
	})
}

func (a *Adler) upgradeWebSocket(r *http.Request, w http.ResponseWriter) (net.Conn, error) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (a *Adler) OnRoomJoin(fn func(*Session, *Room)) {
	a.handlers.onRoomJoin = fn
}

func (a *Adler) OnRoomLeave(fn func(*Session, *Room)) {
	a.handlers.onRoomLeave = fn
}

func (a *Adler) HandleConnect(fn func(*Session)) {
	a.handlers.connectHandler = fn
}

func (a *Adler) HandleDisconnect(fn func(*Session)) {
	a.handlers.disconnectHandler = fn
}

func (a *Adler) HandlePong(fn func(*Session)) {
	a.handlers.pongHandler = fn
}

func (a *Adler) HandleMessage(fn func(*Session, []byte)) {
	a.handlers.messageHandler = fn
}

func (a *Adler) HandleMessageBinary(fn func(*Session, []byte)) {
	a.handlers.messageHandlerBinary = fn
}

func (a *Adler) HandleSentMessage(fn func(*Session, []byte)) {
	a.handlers.messageSentHandler = fn
}

func (a *Adler) HandleSentMessageBinary(fn func(*Session, []byte)) {
	a.handlers.messageSentHandlerBinary = fn
}

func (a *Adler) HandleError(fn func(*Session, error)) {
	a.handlers.errorHandler = fn
}
