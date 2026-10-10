package qtls

// Solovey integration delta against 6a3a24d65b99587fad1d4cdd567c88f212acdd63.
// Protocol authentication remains in its original service. This owner retains
// accepted transport handles and their admission/close lifetime, never flows.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sagernet/quic-go"
)

const ParentDrainTimeout = 2 * time.Second

var ErrParentUnavailable = errors.New("QUIC parent unavailable or identity mismatch")

type Parent[U comparable] struct {
	ID            uint64
	User          U
	Authenticated bool
	CreatedAt     time.Time
}

type parentHookKey[U comparable] struct{}

// WithParentControl is a constructor-scoped hook. It exposes only the service
// created with this context; there is no process-wide observer or catalogue.
func WithParentControl[U comparable](ctx context.Context, hook func(*ParentOwner[U])) context.Context {
	return context.WithValue(ctx, parentHookKey[U]{}, hook)
}

type parentAdmissionKey struct{}

type parentAdmission struct {
	mu        sync.Mutex
	closing   bool
	inflight  int
	quiescent chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	conn      *quic.Conn
	connCtx   context.Context
}

// ParentAdmission fences late routed flows while allowing the existing policy
// and official inventory owner to publish an admission already in progress.
func ParentAdmission(ctx context.Context, accept func()) bool {
	gate, _ := ctx.Value(parentAdmissionKey{}).(*parentAdmission)
	if gate == nil {
		if ctx.Err() != nil {
			return false
		}
		accept()
		return true
	}
	gate.mu.Lock()
	if gate.closing || ctx.Err() != nil || gate.connCtx.Err() != nil {
		gate.mu.Unlock()
		return false
	}
	gate.inflight++
	gate.mu.Unlock()
	defer func() {
		gate.mu.Lock()
		gate.inflight--
		if gate.closing && gate.inflight == 0 {
			gate.cancel()
			close(gate.quiescent)
		}
		gate.mu.Unlock()
	}()
	accept()
	return true
}

func (g *parentAdmission) retire() <-chan struct{} {
	g.mu.Lock()
	if !g.closing {
		g.closing = true
		if g.inflight == 0 {
			g.cancel()
			close(g.quiescent)
		}
	}
	g.mu.Unlock()
	return g.quiescent
}

type liveParent[U comparable] struct {
	Parent[U]
	gate      *parentAdmission
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

type ParentOwner[U comparable] struct {
	ctx        context.Context
	mu         sync.Mutex
	parents    map[*quic.Conn]*liveParent[U]
	nextID     uint64
	closing    bool
	started    bool
	listener   Listener
	stopAccept context.CancelFunc
	acceptDone chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

func NewParentOwner[U comparable](ctx context.Context) *ParentOwner[U] {
	o := &ParentOwner[U]{ctx: ctx, parents: make(map[*quic.Conn]*liveParent[U]), acceptDone: make(chan struct{})}
	if hook, _ := ctx.Value(parentHookKey[U]{}).(func(*ParentOwner[U])); hook != nil {
		hook(o)
	}
	return o
}

// Start records every Accept result before protocol work can run. Cancelling
// Accept and waiting for acceptDone is the drain barrier, including a result
// returned concurrently with shutdown. Closing the acceptor then exposes its
// finite completed-handshake queue; that queue is drained before UDP destruction.
func (o *ParentOwner[U]) Start(listener Listener, handle func(context.Context, *quic.Conn), failed func(error)) error {
	o.mu.Lock()
	if o.closing || o.started {
		o.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), ParentDrainTimeout)
		defer cancel()
		return errors.Join(ErrParentUnavailable, closeListener(ctx, listener))
	}
	acceptCtx, cancel := context.WithCancel(o.ctx)
	o.listener, o.stopAccept, o.started = listener, cancel, true
	o.mu.Unlock()
	go func() {
		defer close(o.acceptDone)
		for {
			conn, err := listener.Accept(acceptCtx)
			if err != nil {
				failed(err)
				return
			}
			ctx, _ := o.adopt(conn)
			o.mu.Lock()
			closing := o.closing
			o.mu.Unlock()
			if closing {
				return
			}
			go handle(ctx, conn)
		}
	}()
	return nil
}

func (o *ParentOwner[U]) adopt(conn *quic.Conn) (context.Context, *liveParent[U]) {
	ctx, cancel := context.WithCancel(o.ctx)
	gate := &parentAdmission{ctx: ctx, cancel: cancel, conn: conn, connCtx: conn.Context(), quiescent: make(chan struct{})}
	ctx = context.WithValue(ctx, parentAdmissionKey{}, gate)
	o.mu.Lock()
	o.nextID++
	peer := &liveParent[U]{Parent: Parent[U]{ID: o.nextID, CreatedAt: time.Now()}, gate: gate, closeDone: make(chan struct{})}
	o.parents[conn] = peer
	o.mu.Unlock()
	context.AfterFunc(conn.Context(), func() {
		gate.retire()
		cancel()
		o.mu.Lock()
		delete(o.parents, conn)
		o.mu.Unlock()
	})
	return ctx, peer
}

// Authenticate is called only after the original protocol verifier succeeded.
// A handle cannot change principal through a second authentication request.
func (o *ParentOwner[U]) Authenticate(conn *quic.Conn, user U) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	peer := o.parents[conn]
	if o.closing || peer == nil || conn.Context().Err() != nil {
		return false
	}
	peer.gate.mu.Lock()
	defer peer.gate.mu.Unlock()
	if peer.gate.closing || peer.gate.ctx.Err() != nil || peer.Authenticated {
		return false
	}
	peer.User, peer.Authenticated = user, true
	return true
}

func (o *ParentOwner[U]) Snapshot(limit int) ([]Parent[U], int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]Parent[U], 0, min(max(limit, 0), len(o.parents)))
	total := 0
	for conn, peer := range o.parents {
		peer.gate.mu.Lock()
		live := peer.Authenticated && !peer.gate.closing && conn.Context().Err() == nil
		peer.gate.mu.Unlock()
		if live {
			total++
			if len(result) < limit {
				result = append(result, peer.Parent)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, total
}

func (o *ParentOwner[U]) Get(id uint64) (Parent[U], bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for conn, peer := range o.parents {
		if peer.ID == id && peer.Authenticated && conn.Context().Err() == nil {
			peer.gate.mu.Lock()
			live := !peer.gate.closing
			peer.gate.mu.Unlock()
			if live {
				return peer.Parent, true
			}
		}
	}
	return Parent[U]{}, false
}

func closeParent[U comparable](ctx context.Context, peer *liveParent[U], reason string) error {
	quiescent := peer.gate.retire()
	peer.closeOnce.Do(func() {
		go func() {
			defer close(peer.closeDone)
			drainCtx, cancel := context.WithTimeout(context.Background(), ParentDrainTimeout)
			defer cancel()
			select {
			case <-quiescent:
			case <-drainCtx.Done():
				peer.closeErr = drainCtx.Err()
				peer.gate.cancel()
			}
			// A cancelled caller does not abandon the initiated close. Pinned
			// quic-go waits for its CONNECTION_CLOSE emission attempt and run
			// cleanup before cancelling Conn.Context. A drain timeout is retained.
			peer.closeErr = errors.Join(peer.closeErr, peer.gate.conn.CloseWithError(0, reason))
		}()
	})
	select {
	case <-peer.closeDone:
		return peer.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *ParentOwner[U]) CloseParent(ctx context.Context, id uint64, user U) error {
	o.mu.Lock()
	var selected *liveParent[U]
	for _, peer := range o.parents {
		if peer.ID == id && peer.Authenticated && peer.User == user && !o.closing {
			selected = peer
			break
		}
	}
	o.mu.Unlock()
	if selected == nil {
		return ErrParentUnavailable
	}
	closeCtx, cancel := context.WithTimeout(ctx, ParentDrainTimeout)
	defer cancel()
	err := closeParent(closeCtx, selected, "session disconnected")
	o.removeClosed()
	return err
}

func (o *ParentOwner[U]) removeClosed() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for conn := range o.parents {
		if conn.Context().Err() != nil {
			delete(o.parents, conn)
		}
	}
}

func (o *ParentOwner[U]) Close() error {
	o.closeOnce.Do(func() { o.closeErr = o.drain() })
	return o.closeErr
}

func (o *ParentOwner[U]) drain() error {
	ctx, cancel := context.WithTimeout(context.Background(), ParentDrainTimeout)
	defer cancel()
	o.mu.Lock()
	o.closing = true
	started, listener, stopAccept := o.started, o.listener, o.stopAccept
	o.mu.Unlock()
	if !started {
		return nil
	}
	stopAccept()
	select {
	case <-o.acceptDone:
	case <-ctx.Done():
		return errors.Join(fmt.Errorf("QUIC acceptance drain: %w", ctx.Err()), closeListener(ctx, listener))
	}
	o.mu.Lock()
	peers := make([]*liveParent[U], 0, len(o.parents))
	for _, peer := range o.parents {
		peer.gate.retire()
		peers = append(peers, peer)
	}
	o.mu.Unlock()
	drainErr := closeParents(ctx, peers)
	// Public quic-go Close stops handshakes, but preserves established handles.
	// Accept still returns every queued connection before ErrServerClosed. Adopt
	// and close those handles through this same owner without protocol admission.
	listenerErr := closeListener(ctx, listener)
	if listenerErr == nil {
		for {
			conn, err := listener.Accept(ctx)
			if err != nil {
				if !errors.Is(err, quic.ErrServerClosed) {
					drainErr = errors.Join(drainErr, fmt.Errorf("QUIC queued acceptance drain: %w", err))
				}
				break
			}
			_, peer := o.adopt(conn)
			if err := closeParent(ctx, peer, "inbound closed"); err != nil {
				drainErr = errors.Join(drainErr, err)
			}
		}
	}
	o.removeClosed()
	return errors.Join(drainErr, listenerErr)
}

func closeListener(ctx context.Context, listener Listener) error {
	done := make(chan error, 1)
	go func() { done <- listener.Close() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("QUIC listener close: %w", ctx.Err())
	}
}

func closeParents[U comparable](ctx context.Context, peers []*liveParent[U]) error {
	// Eight workers bound close fanout. A deadline is a reported failure, never
	// an assertion of network delivery or permission to wait without a bound.
	jobs := make(chan *liveParent[U])
	var workers sync.WaitGroup
	var errMu sync.Mutex
	var drainErr error
	for i := 0; i < min(8, len(peers)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for peer := range jobs {
				if err := closeParent(ctx, peer, "inbound closed"); err != nil {
					errMu.Lock()
					drainErr = errors.Join(drainErr, err)
					errMu.Unlock()
				}
			}
		}()
	}
enqueue:
	for _, peer := range peers {
		select {
		case jobs <- peer:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait() // workers select on the same bounded context
	if ctx.Err() != nil {
		drainErr = errors.Join(drainErr, ctx.Err())
	}
	return drainErr
}
