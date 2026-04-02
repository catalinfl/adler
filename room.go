package adler

import "sync"

type Room struct {
	mu       sync.RWMutex
	name     string
	sessions map[*Session]struct{}
}
