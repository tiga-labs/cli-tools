package chromeattach

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

// Browser is a connection to the user's Chrome.
type Browser struct {
	conn *websocket.Conn

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan reply

	done      chan struct{} // closed when the read loop stops
	closeOnce sync.Once
	closeErr  error
}

type reply struct {
	Result json.RawMessage
	Err    *CDPError
}

// CDPError is an error answer from Chrome to one CDP call.
type CDPError struct {
	Method  string
	Code    int
	Message string
}

func (e *CDPError) Error() string {
	return fmt.Sprintf("%s: %s (%d)", e.Method, e.Message, e.Code)
}

func newBrowser(conn *websocket.Conn) *Browser {
	b := &Browser{conn: conn, pending: map[int64]chan reply{}, done: make(chan struct{})}
	go b.readLoop()
	return b
}

// readLoop hands each reply to the call waiting for its id. Events (no id) and replies
// nobody waits for are dropped. When the connection ends, every waiting call fails.
func (b *Browser) readLoop() {
	defer close(b.done)
	for {
		var msg struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := b.conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.ID == 0 {
			continue
		}
		b.mu.Lock()
		ch, ok := b.pending[msg.ID]
		delete(b.pending, msg.ID)
		b.mu.Unlock()
		if !ok {
			continue
		}
		r := reply{Result: msg.Result}
		if msg.Error != nil {
			r.Err = &CDPError{Code: msg.Error.Code, Message: msg.Error.Message}
		}
		ch <- r
	}
}

// call sends one CDP request (on a tab session when sessionID is set) and decodes the
// reply's result into result, which may be nil.
func (b *Browser) call(ctx context.Context, sessionID, method string, params any, result any) error {
	select {
	case <-b.done:
		return ErrConnectionClosed
	default:
	}

	ch := make(chan reply, 1)
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.pending[id] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
	}()

	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	b.writeMu.Lock()
	err := b.conn.WriteJSON(msg)
	b.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConnectionClosed, err)
	}

	select {
	case r := <-ch:
		if r.Err != nil {
			r.Err.Method = method
			return r.Err
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(r.Result, result); err != nil {
			return fmt.Errorf("%s: decode result: %w", method, err)
		}
		return nil
	case <-b.done:
		return ErrConnectionClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the connection. It never closes Chrome. Calling it again is a no-op.
func (b *Browser) Close() error {
	b.closeOnce.Do(func() {
		b.closeErr = b.conn.Close()
		<-b.done
	})
	return b.closeErr
}
