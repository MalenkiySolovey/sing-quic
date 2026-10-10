package tuic

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observedEarlyConn struct {
	net.Conn
	readEntered chan struct{}
	readOnce    sync.Once
	closes      atomic.Int32
}

func (c *observedEarlyConn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() { close(c.readEntered) })
	return c.Conn.Read(p)
}
func (c *observedEarlyConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

func TestEarlyDataOrderAndConcurrentClose(t *testing.T) {
	local, remote := net.Pipe()
	t.Cleanup(func() { _ = remote.Close() })
	underlying := &observedEarlyConn{Conn: local, readEntered: make(chan struct{})}
	conn := &earlyDataConn{Conn: underlying, initial: []byte("initial")}
	initial := make([]byte, 7)
	if _, err := io.ReadFull(conn, initial); err != nil || !bytes.Equal(initial, []byte("initial")) {
		t.Fatal("initial request payload changed", err)
	}
	readDone := make(chan error, 1)
	go func() { _, err := conn.Read(make([]byte, 1)); readDone <- err }()
	<-underlying.readEntered
	var closers sync.WaitGroup
	for range 16 {
		closers.Add(1)
		go func() { defer closers.Done(); _ = conn.Close() }()
	}
	closers.Wait()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("close did not interrupt underlying read")
		}
	case <-time.After(time.Second):
		t.Fatal("close blocked behind network read")
	}
	if underlying.closes.Load() != 1 || conn.initial != nil {
		t.Fatal("stream cleanup was not exactly once")
	}
}

func TestEarlyDataReadCloseRace(t *testing.T) {
	for range 100 {
		local, remote := net.Pipe()
		conn := &earlyDataConn{Conn: local, initial: []byte("initial")}
		barrier := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() { defer workers.Done(); <-barrier; _, _ = conn.Read(make([]byte, 7)) }()
		go func() { defer workers.Done(); <-barrier; _ = conn.Close() }()
		close(barrier)
		workers.Wait()
		_ = remote.Close()
	}
}
