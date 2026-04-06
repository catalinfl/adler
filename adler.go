package adler

import (
	"net"
	"net/http"
	"sync"

	"github.com/gobwas/ws"
)

type messageFunc func(*Session, []byte)
type errorFunc func(*Session, error)
type closeFunc func(*Session, int, string) error
type sessionFunc func(*Session)
type filterFunc func(*Session) bool

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
}

const LargeBuffer int = 1024
const MediumBuffer int = 512
const SmallBuffer int = 256

type Adler struct {
	Config   *Config
	hub      *hub
	handlers handlers
}

func (a *Adler) New() *Adler {
	// no handler functions are initialized by default, all are nil
	handlers := handlers{}

	return &Adler{
		Config:   newConfig(),
		hub:      newHub(),
		handlers: handlers,
	}
}

func (a *Adler) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	if a.hub.isClosed() {
		return ErrHubClosed
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
		Keys:       make(map[string]any),
		Request:    r,
		Protocol:   r.Proto,
		Conn:       conn,
		output:     make(chan message, outputBufferSize),
		outputDone: make(chan struct{}),
		mu:         sync.RWMutex{},
		closed:     false,
		adler:      a,
	}

	if err := a.hub.register(session); err != nil {
		_ = conn.Close()
		return err
	}

	a.handlers.connectHandler(session)
	go session.writePump()
	session.readPump()

	if !a.hub.isClosed() {
		a.hub.unregister(session)
	}

	session.close()
	a.handlers.disconnectHandler(session)
	return nil
}

func (a *Adler) upgradeWebSocket(r *http.Request, w http.ResponseWriter) (net.Conn, error) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return nil, err
	}
	return conn, nil
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
