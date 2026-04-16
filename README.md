# Adler

Adler is a lightweight WebSocket server toolkit for Go. It gives you a small, focused API for upgrading HTTP requests, handling sessions, broadcasting messages, and organizing clients into rooms.

## Features

- WebSocket upgrade from `net/http`
- Session lifecycle hooks
- Text, binary, and JSON messaging
- Global broadcast, filtered broadcast, and targeted session sends
- Rooms with join, leave, and room-level broadcast helpers
- Per-session key-value storage
- Optional access to the underlying request, protocol, and connection

## Installation

```bash
go get github.com/catalinfl/adler
```

## Quick Start

```go
package main

import (
    "log"
    "net/http"

    "github.com/catalinfl/adler"
)

func main() {
    a := adler.New()

    a.HandleConnect(func(s *adler.Session) {
        log.Println("connected:", s.RemoteAddr())
    })

    a.HandleMessage(func(s *adler.Session, msg []byte) {
        _ = s.WriteText([]byte("echo: " + string(msg)))
    })

    a.HandleError(func(s *adler.Session, err error) {
        log.Println("ws error:", err)
    })

    http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
        if err := a.HandleRequest(w, r); err != nil {
            log.Println("handle request:", err)
        }
    })

    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

## How The API Works

### 1. Create a server

Use `adler.New()` to build a server instance. You can pass configuration options at construction time:

```go
a := adler.New(
    adler.WithDispatchAsync(true),
    adler.WithMessageBufferSize(256),
    adler.WithPingPeriod(30*time.Second),
)
```

### 2. Register handlers before serving requests

Handlers are set on the `Adler` instance and are called during the session lifecycle.

- `HandleConnect` runs after a session is registered.
- `HandleMessage` receives text frames.
- `HandleMessageBinary` receives binary frames.
- `HandlePong` receives pong frames.
- `HandleClose` receives close code and reason.
- `HandleSentMessage` and `HandleSentMessageBinary` run after server writes succeed.
- `HandleError` receives runtime errors from the session loop.
- `OnRoomJoin` and `OnRoomLeave` receive room membership events.

Example:

```go
a.HandleConnect(func(s *adler.Session) {
    s.Set("userID", "123")
})

a.HandleClose(func(s *adler.Session, code int, reason string) {
    log.Printf("client closed: code=%d reason=%q", code, reason)
})
```

### 3. Serve the websocket endpoint

Call `HandleRequest` from an HTTP handler. It upgrades the connection and blocks until the session ends.

```go
http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
    _ = a.HandleRequest(w, r)
})
```

### 4. Work with a Session

`Session` is the object you receive in callbacks. It exposes message writers and a small key-value store.

Messaging helpers:

- `WriteText([]byte)`
- `WriteTextWithDeadline([]byte, time.Duration)`
- `WriteBinary([]byte)`
- `WriteBinaryWithDeadline([]byte, time.Duration)`
- `WriteJSON(adler.Map)`
- `WriteJSONWithDeadline(any, time.Duration)`
- `Close(...[]byte)`

Storage helpers:

- `Set(key, value)`
- `SetNX(key, value)`
- `Get(key)`
- `GetString(key)`
- `GetInt(key)`
- `GetInt64(key)`
- `GetFloat(key)`
- `GetBool(key)`
- `Has(key)`
- `Unset(key)`
- `Keys()`
- `Values()`
- `Clear()`
- `Incr(key)` and `Decr(key)` for `*int64` counters

Metadata:

- `Request()` returns the original HTTP request
- `Protocol()` returns the HTTP protocol string used during upgrade
- `LocalAddr()` and `RemoteAddr()` expose the connection addresses
- `Room()` returns the current room
- `UnsafeConn()` exposes the raw network connection when you need low-level control

Example session storage:

```go
a.HandleConnect(func(s *adler.Session) {
    s.Set("role", "admin")
    s.SetNX("seen", true)
})

a.HandleMessage(func(s *adler.Session, msg []byte) {
    role, _ := s.GetString("role")
    _ = s.WriteText([]byte("role=" + role + " msg=" + string(msg)))
})
```

### 5. Broadcast to all or some clients

Use server-level broadcast helpers when you want to send to multiple sessions.

- `Broadcast([]byte)` sends text to all connected sessions
- `BroadcastFilter([]byte, func(*Session) bool)` sends text to matching sessions
- `BroadcastBinary([]byte)` sends binary to all connected sessions
- `BroadcastBinaryFilter([]byte, func(*Session) bool)` sends binary to matching sessions
- `BroadcastJSON(adler.Map)` broadcasts JSON
- `BroadcastJSONFilter(adler.Map, func(*Session) bool)` broadcasts JSON to matching sessions
- `BroadcastOthers([]byte, *Session)` sends to everyone except the target session
- `SendTo([]byte, *Session)` sends only to one session

Example:

```go
a.Broadcast([]byte("server says hello"))

a.SendTo([]byte("private message"), session)

a.BroadcastFilter([]byte("admins only"), func(s *adler.Session) bool {
    role, _ := s.GetString("role")
    return role == "admin"
})
```

### 6. Group clients in rooms

Rooms help you manage subsets of sessions.

```go
room := a.NewRoom("lobby")

a.HandleConnect(func(s *adler.Session) {
    _ = room.Join(s)
})

room.Broadcast([]byte("welcome to the lobby"))
```

Room helpers:

- `NewRoom(name)` returns an existing room or creates one
- `Name()` returns the room name
- `Len()` returns the number of members
- `Sessions()` returns a snapshot of current members
- `Join(*Session)` adds a session to the room
- `Leave(*Session)` removes a session
- `OpenRoom()` allows joins again
- `CloseRoom()` blocks new joins
- `Broadcast`, `BroadcastBinary`, `BroadcastFilter`, `BroadcastJSON`, `BroadcastJSONFilter`

### 7. Close handling

If the client sends a close frame, `HandleClose` receives the close status code and reason.

```go
a.HandleClose(func(s *adler.Session, code int, reason string) {
    log.Printf("close: code=%d reason=%q", code, reason)
})
```

## Configuration

Use these options with `adler.New(...)`:

- `WithWriteWait(time.Duration)` interprets the argument as seconds; `WithWriteWait(10)` means 10 seconds
- `WithPongWait(time.Duration)` interprets the argument as seconds; `WithPongWait(60)` means 60 seconds, and `WithPongWait(0)` disables idle disconnects
- `WithPingPeriod(time.Duration)` interprets the argument as seconds; `WithPingPeriod(54)` means 54 seconds
- `WithMessageBufferSize(int)` sets the outbound queue size; start with 64-256 and increase only if you hit `ErrBufferFull` under normal bursts
- `WithDispatchAsync(bool)` switches inbound dispatch to goroutine-per-message when enabled

## Notes On Concurrency

- Session storage is protected by an internal mutex.
- Server broadcast methods are safe for normal concurrent use.
- `UnsafeConn()` bypasses Adler's internal coordination; only use it if you manage access carefully.

## Minimal Room Example

```go
room := a.NewRoom("test")

a.HandleConnect(func(s *adler.Session) {
    _ = room.Join(s)
})

room.HandleJoin(func(s *adler.Session) {
    _ = s.WriteText([]byte("joined room"))
})
```

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for the full text.