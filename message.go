package adler

import (
	"time"

	"github.com/gobwas/ws"
)

type message struct {
	messageType ws.OpCode
	content     []byte
	filter      filterFunc
	writeWait   time.Duration
}
