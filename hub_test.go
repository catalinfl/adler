package adler

import (
	"fmt"
	"testing"

	"github.com/gobwas/ws"
)

func BenchmarkHubBroadcast(b *testing.B) {
	const payloadSize = 1024
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	errNoop := func(*Session, error) {}

	for _, sessionCount := range []int{1, 16, 128, 512} {
		b.Run(fmt.Sprintf("sessions_%d", sessionCount), func(b *testing.B) {
			h := newHub()
			stopDrain := make(chan struct{})

			for i := 0; i < sessionCount; i++ {
				s := &Session{
					output:     make(chan message, 256),
					outputDone: make(chan struct{}),
					adler: &Adler{
						handlers: handlers{errorHandler: errNoop},
					},
				}

				if err := h.register(s); err != nil {
					b.Fatalf("register session: %v", err)
				}

				go func(session *Session) {
					for {
						select {
						case <-session.output:
						case <-stopDrain:
							return
						}
					}
				}(s)
			}

			b.Cleanup(func() {
				close(stopDrain)
			})

			msg := message{
				messageType: ws.OpText,
				content:     payload,
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(payload) * sessionCount))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				h.broadcast(msg)
			}
		})
	}
}
