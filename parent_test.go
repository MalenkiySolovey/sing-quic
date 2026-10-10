package qtls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
)

func TestParentAdmissionCloseBarrier(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	gate := &parentAdmission{ctx: ctx, cancel: cancel, connCtx: context.Background(), quiescent: make(chan struct{})}
	ctx = context.WithValue(ctx, parentAdmissionKey{}, gate)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	go func() { done <- ParentAdmission(ctx, func() { close(entered); <-release }) }()
	<-entered
	quiescent := gate.retire()
	select {
	case <-quiescent:
		t.Fatal("in-progress admission was not drained")
	default:
	}
	if ParentAdmission(ctx, func() { t.Error("late rejected admission reached accounting") }) {
		t.Fatal("closing parent admitted")
	}
	close(release)
	if !<-done {
		t.Fatal("accepted admission changed retroactively")
	}
	select {
	case <-quiescent:
	case <-time.After(time.Second):
		t.Fatal("admission drain leaked")
	}
	if ctx.Err() == nil {
		t.Fatal("closed parent context survived")
	}
}

type idleParentListener struct {
	closes     atomic.Int32
	accepted   chan struct{}
	acceptOnce sync.Once
}

func (l *idleParentListener) Accept(ctx context.Context) (*quic.Conn, error) {
	if l.closes.Load() != 0 {
		return nil, quic.ErrServerClosed
	}
	l.acceptOnce.Do(func() { close(l.accepted) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (l *idleParentListener) Close() error   { l.closes.Add(1); return nil }
func (l *idleParentListener) Addr() net.Addr { return &net.UDPAddr{} }

func TestParentOwnerPartialStartupAndCloseOnce(t *testing.T) {
	var hook *ParentOwner[int]
	owner := NewParentOwner[int](WithParentControl(t.Context(), func(value *ParentOwner[int]) { hook = value }))
	if owner != hook {
		t.Fatal("constructor hook changed lifecycle owner")
	}
	listener := &idleParentListener{accepted: make(chan struct{})}
	if err := owner.Start(listener, func(context.Context, *quic.Conn) { t.Error("unexpected parent") }, func(error) {}); err != nil {
		t.Fatal(err)
	}
	<-listener.accepted
	var closers sync.WaitGroup
	for range 16 {
		closers.Add(1)
		go func() {
			defer closers.Done()
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	closers.Wait()
	if listener.closes.Load() != 1 {
		t.Fatal("listener was not closed exactly once")
	}
	late := &idleParentListener{accepted: make(chan struct{})}
	if err := owner.Start(late, nil, nil); !errors.Is(err, ErrParentUnavailable) || late.closes.Load() != 1 {
		t.Fatal("late startup cleanup missing")
	}
	cold := NewParentOwner[int](t.Context())
	if err := cold.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cold.Close(); err != nil {
		t.Fatal(err)
	}
}

type orderedParentListener struct {
	Listener
	owner    *ParentOwner[int]
	closes   atomic.Int32
	violated atomic.Bool
}

func (l *orderedParentListener) Close() error {
	l.owner.mu.Lock()
	for conn := range l.owner.parents {
		if conn.Context().Err() == nil {
			l.violated.Store(true)
		}
	}
	l.owner.mu.Unlock()
	l.closes.Add(1)
	return l.Listener.Close()
}

func parentTestListener(t *testing.T) (*quic.Listener, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"parent-fixture.invalid"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	server, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, NextProtos: []string{"parent-fixture"}, MinVersion: tls.VersionTLS13}, &quic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, &tls.Config{RootCAs: pool, ServerName: "parent-fixture.invalid", NextProtos: []string{"parent-fixture"}, MinVersion: tls.VersionTLS13}
}

func TestParentOwnerLiveDrainAndBoundedRegistry(t *testing.T) {
	server, clientTLS := parentTestListener(t)
	owner := NewParentOwner[int](t.Context())
	listener := &orderedParentListener{Listener: server, owner: owner}
	authenticated := make(chan *quic.Conn, 24)
	if err := owner.Start(listener, func(_ context.Context, conn *quic.Conn) {
		if owner.Authenticate(conn, 1) {
			authenticated <- conn
		}
	}, func(error) {}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	clients := make([]*quic.Conn, 0, 24)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for range 24 {
		conn, err := quic.DialAddr(ctx, server.Addr().String(), clientTLS, &quic.Config{})
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, conn)
		t.Cleanup(func() { _ = conn.CloseWithError(0, "") })
		select {
		case <-authenticated:
		case <-ctx.Done():
			t.Fatal("authentication barrier timed out")
		}
	}
	snapshot, total := owner.Snapshot(2)
	if total != 24 || len(snapshot) != 2 {
		t.Fatal("snapshot bound incorrect")
	}
	if err := owner.CloseParent(ctx, snapshot[0].ID, 2); !errors.Is(err, ErrParentUnavailable) {
		t.Fatal("foreign principal close accepted")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if listener.violated.Load() || listener.closes.Load() != 1 {
		t.Fatal("listener destroyed before parent close emission")
	}
	for _, conn := range clients {
		select {
		case <-conn.Context().Done():
			var app *quic.ApplicationError
			if !errors.As(context.Cause(conn.Context()), &app) || !app.Remote {
				t.Fatal("no remote CONNECTION_CLOSE")
			}
		case <-ctx.Done():
			t.Fatal("parent close timed out")
		}
	}
	owner.mu.Lock()
	retained := len(owner.parents)
	owner.mu.Unlock()
	if retained != 0 {
		t.Fatal("completed parents retained in registry")
	}
	if err := owner.Close(); err != nil || listener.closes.Load() != 1 {
		t.Fatal("repeated close changed ownership")
	}
}

// Model the public Accept contract deterministically: one completed handle wins
// the cancellation race; the finite remaining queue is returned after Close.
type queuedParentListener struct {
	Listener
	parents []*quic.Conn
	entered chan struct{}
	index   int
	closed  atomic.Bool
	closes  atomic.Int32
}

func (l *queuedParentListener) Accept(ctx context.Context) (*quic.Conn, error) {
	if l.index == 0 {
		close(l.entered)
		<-ctx.Done()
	} else if !l.closed.Load() {
		return nil, errors.New("queue drained before acceptor stopped")
	}
	if l.index == len(l.parents) {
		return nil, quic.ErrServerClosed
	}
	conn := l.parents[l.index]
	l.index++
	return conn, nil
}

func (l *queuedParentListener) Close() error {
	l.closes.Add(1)
	err := l.Listener.Close()
	l.closed.Store(true)
	return err
}

func TestParentOwnerLateAcceptAndCompletedQueue(t *testing.T) {
	server, clientTLS := parentTestListener(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	listener := &queuedParentListener{Listener: server, entered: make(chan struct{})}
	var clients []*quic.Conn
	for range 3 {
		client, err := quic.DialAddr(ctx, server.Addr().String(), clientTLS, &quic.Config{})
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
		t.Cleanup(func() { _ = client.CloseWithError(0, "") })
		parent, err := server.Accept(ctx)
		if err != nil {
			t.Fatal(err)
		}
		listener.parents = append(listener.parents, parent)
	}
	owner := NewParentOwner[int](t.Context())
	if err := owner.Start(listener, func(context.Context, *quic.Conn) { t.Error("closing parent reached protocol admission") }, func(error) {}); err != nil {
		t.Fatal(err)
	}
	<-listener.entered
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		select {
		case <-client.Context().Done():
			var app *quic.ApplicationError
			if !errors.As(context.Cause(client.Context()), &app) || !app.Remote {
				t.Fatal("queued parent did not observe remote close")
			}
		case <-ctx.Done():
			t.Fatal("queued parent close timed out")
		}
	}
	owner.mu.Lock()
	retained := len(owner.parents)
	owner.mu.Unlock()
	if listener.closes.Load() != 1 || listener.index != 3 || retained != 0 {
		t.Fatal("late queue cleanup was incomplete")
	}
}

type blockedCloseListener struct {
	idleParentListener
	entered chan struct{}
	release chan struct{}
}

func (l *blockedCloseListener) Close() error { close(l.entered); <-l.release; return nil }

func TestListenerCloseDeadline(t *testing.T) {
	listener := &blockedCloseListener{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	defer close(listener.release)
	if err := closeListener(ctx, listener); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("listener wait was unbounded", err)
	}
	select {
	case <-listener.entered:
	default:
		t.Fatal("bounded waiter abandoned cleanup")
	}
}
