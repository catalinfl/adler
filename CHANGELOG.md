# Changelog

All notable changes for this project are documented here.

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