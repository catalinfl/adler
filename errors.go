package adler

import "errors"

var (
	ErrKeyNotFound         = errors.New("key does not exist")
	ErrCoreClosed          = errors.New("core is closed")
	ErrNilSession          = errors.New("nil session")
	ErrAlreadyRegistered   = errors.New("session is already registered")
	ErrWriteClosed         = errors.New("write session is closed")
	ErrBufferFull          = errors.New("buffer is full")
	ErrSessionClosed       = errors.New("session is closed")
	ErrTypeAssertionFailed = errors.New("type assertion failed")
	ErrRoomClosed          = errors.New("room is closed")
	ErrCoreExit            = errors.New("cannot exit from core")
)
