package tuic

import (
	"net"
	"sync"
)

// The request parser may also read a small initial payload. Keep its lifetime
// local to this stream: concurrent Close must not release the reader's buffer.
// Underlying network reads never hold mu, so Close can unblock them normally.
type earlyDataConn struct {
	net.Conn
	mu        sync.Mutex
	initial   []byte
	closeOnce sync.Once
	closeErr  error
}

func (c *earlyDataConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.initial) != 0 {
		n := copy(p, c.initial)
		c.initial = c.initial[n:]
		c.mu.Unlock()
		return n, nil
	}
	c.mu.Unlock()
	return c.Conn.Read(p)
}

func (c *earlyDataConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.Conn.Close()
		c.mu.Lock()
		c.initial = nil
		c.mu.Unlock()
	})
	return c.closeErr
}
