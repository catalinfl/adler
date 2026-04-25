# Changelog

All notable changes for this project are documented here.

## Roadmap

- [ ] Add a dedicated matchmaker module for queue creation, queue management, and match orchestration.
- [ ] Implement matchmaking based on ELO ranking, including configurable ranking buckets and match quality rules.

## 1.1.0

### Added

- Room lifecycle configuration option: `WithDeleteRoomOnEmpty(bool)` (default `true`)
- Manual room removal API: `Adler.DeleteRoom(name)`
- New error for manual delete safety: `ErrRoomNotEmpty`

### Changed

- `PongWait` default is now `0` (idle timeout disabled by default)
- `PingPeriod` default is now `60` seconds
- Time options are interpreted as seconds in option helpers:
  - `WithWriteWait(x)` -> `x` seconds
  - `WithPongWait(x)` -> `x` seconds
  - `WithPingPeriod(x)` -> `x` seconds
- Room lookup now uses read locking in `Adler.Room(...)`

### Fixed

- Fixed room cleanup behavior to respect `WithDeleteRoomOnEmpty(false)` and keep empty rooms when configured

### Migration Notes

- If you need idle disconnects, set `WithPongWait(...)` explicitly to a value greater than `0`.
- If you relied on duration-style calls like `WithPingPeriod(30*time.Second)`, update to seconds-style calls like `WithPingPeriod(30)`.

## 1.0.0

### Added

- WebSocket upgrade and session lifecycle management through `Adler.HandleRequest`
- Session hooks: `HandleConnect`, `HandleDisconnect`, `HandleMessage`, `HandleMessageBinary`, `HandlePong`, `HandleClose`, `HandleError`
- Sent-message hooks: `HandleSentMessage` and `HandleSentMessageBinary`
- Room lifecycle hooks: `OnRoomJoin` and `OnRoomLeave`
- Session message writers for text, binary, JSON, and deadline-aware variants
- Session store helpers:
  - `Set`, `SetNX`, `Get`, `GetString`, `GetInt`, `GetInt64`, `GetFloat`, `GetBool`
  - `Has`, `Unset`, `Keys`, `Values`, `Clear`
  - `Incr`, `Decr`
- Session metadata accessors:
  - `Request`, `Protocol`, `LocalAddr`, `RemoteAddr`, `Room`, `Identity`
  - `UnsafeConn` for low-level access when needed
- Global broadcast helpers:
  - `Broadcast`, `BroadcastFilter`
  - `BroadcastBinary`, `BroadcastBinaryFilter`
  - `BroadcastJSON`, `BroadcastJSONFilter`
  - `BroadcastOthers`, `SendTo`
- Room API:
  - `NewRoom`, `Name`, `Len`, `Sessions`
  - `Join`, `Leave`, `OpenRoom`, `CloseRoom`
  - `Broadcast`, `BroadcastBinary`, `BroadcastFilter`, `BroadcastJSON`, `BroadcastJSONFilter`
- Runtime options:
  - `WithWriteWait`
  - `WithPongWait`
  - `WithPingPeriod`
  - `WithMessageBufferSize`
  - `WithDispatchAsync`