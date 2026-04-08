package adler

import "errors"

var (
	ErrKeyNotFound       = errors.New("key does not exist")
	ErrCoreClosed        = errors.New("core is closed")
	ErrNilSession        = errors.New("nil session")
	ErrAlreadyRegistered = errors.New("session is already registered")
	ErrWriteClosed       = errors.New("Write session is closed")
	ErrBufferFull        = errors.New("Buffer is full")
	ErrSessionClosed     = errors.New("session is closed")
)
