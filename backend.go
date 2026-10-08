package ntvideo

import (
	"errors"
	"image"
	"sync"
	"time"
)

// backend is owned by the engine. Implementations must return promptly from
// Poll; they must not queue an unbounded backlog of decoded video frames.
// Frame returns an owned immutable image; DOM backends return nil.
type backend interface {
	Load(Source) error
	Play() error
	Pause() error
	Seek(time.Duration) error
	SetVolume(float64) error
	SetMuted(bool) error
	SelectTrack(kind, id string) error
	Layout(CellRect, bool) error
	SetFit(FitMode) error
	Poll() (Status, error)
	Frame(width, height int) (image.Image, error)
	Close() error
}
type operation func(backend) error

// The queue is populated on the Bubble Tea event loop. A single command drains
// it in order, polls state, and renders. Close serializes with that command.
type engine struct {
	mu      sync.Mutex
	queueMu sync.Mutex
	b       backend
	cfg     config
	pending []operation
	closed  bool
	dead    bool // This build can never create a backend; operations are dropped.
	lastErr error
}

func (e *engine) enqueue(op operation) {
	e.queueMu.Lock()
	defer e.queueMu.Unlock()
	if !e.closed && !e.dead {
		e.pending = append(e.pending, op)
	}
}
func (e *engine) sample(w, h int) (s Status, img image.Image) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queueMu.Lock()
	closed := e.closed
	pending := e.pending
	e.pending = nil
	e.queueMu.Unlock()
	if closed {
		return Status{State: Closed, Err: ErrClosed}, nil
	}
	var err error
	if e.b == nil {
		e.b, err = newBackend(e.cfg)
		if err != nil {
			e.queueMu.Lock()
			if errors.Is(err, ErrUnsupported) {
				// Nothing queued can ever run; stop retaining it.
				e.dead = true
				e.pending = nil
			} else {
				// Preserve queued controls for a host/backend that becomes available.
				e.pending = append(pending, e.pending...)
			}
			e.queueMu.Unlock()
			return Status{State: Failed, Err: err}, nil
		}
	}
	if len(pending) > 0 {
		e.lastErr = nil
	}
	for _, op := range pending {
		if next := op(e.b); next != nil {
			err = next
		}
	}
	s, pollErr := e.b.Poll()
	if pollErr != nil {
		err = pollErr
	}
	if w > 0 && h > 0 {
		var frameErr error
		img, frameErr = e.b.Frame(w, h)
		if frameErr != nil {
			err = frameErr
		}
	}
	if err != nil {
		e.lastErr = err
	}
	if e.lastErr != nil {
		s.Err = e.lastErr
		s.State = Failed
	}
	return s, img
}
func (e *engine) close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queueMu.Lock()
	if e.closed {
		e.queueMu.Unlock()
		return nil
	}
	e.closed = true
	e.pending = nil
	e.queueMu.Unlock()
	if e.b != nil {
		return e.b.Close()
	}
	return nil
}
