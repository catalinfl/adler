package adler

import "time"

type message struct {
	// t         int
	content   []byte
	filter    filterFunc
	writeWait time.Duration
}
