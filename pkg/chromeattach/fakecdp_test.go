package chromeattach

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// recordedCall is one CDP request the fake server received.
type recordedCall struct {
	Method    string
	Params    map[string]any
	SessionID string
}

// fakeCDP is an in-process stand-in for Chrome's browser-level DevTools WebSocket.
type fakeCDP struct {
	t   *testing.T
	srv *httptest.Server

	// deny answers the handshake with 403, like a click on Deny.
	deny bool
	// handshakeDelay holds the handshake open, like a prompt nobody has answered.
	handshakeDelay time.Duration
	// evalFn answers Runtime.evaluate. A non-empty exception becomes exceptionDetails.
	evalFn func(expr string) (value any, exception string)
	// createDelay holds the Target.createTarget reply, like a slow Chrome.
	createDelay time.Duration
	// evalCDPErrors answers this many Runtime.evaluate calls with a CDP error first,
	// like a page whose execution context is being replaced during navigation.
	evalCDPErrors atomic.Int32

	mu      sync.Mutex
	methods []recordedCall
	conns   []*websocket.Conn
}

const fakeBrowserPath = "/devtools/browser/fake-guid"

func newFakeCDP(t *testing.T) *fakeCDP {
	t.Helper()
	f := &fakeCDP{t: t}
	upgrader := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.handshakeDelay > 0 {
			select {
			case <-time.After(f.handshakeDelay):
			case <-r.Context().Done():
				return
			}
		}
		if f.deny || r.URL.Path != fakeBrowserPath {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, conn)
		f.mu.Unlock()
		go f.serve(conn)
	}))
	t.Cleanup(func() {
		f.closeConns()
		f.srv.Close()
	})
	return f
}

func (f *fakeCDP) port() string {
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(f.srv.URL, "http://"))
	return port
}

func (f *fakeCDP) path() string { return fakeBrowserPath }

// writePortFile writes a DevToolsActivePort file in dir that points at this server.
func (f *fakeCDP) writePortFile(dir string) {
	f.t.Helper()
	writePortFile(f.t, dir, f.port(), f.path())
}

func writePortFile(t *testing.T, dir, port, path string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(port+"\n"+path+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// closeConns drops every open WebSocket, like Chrome quitting.
func (f *fakeCDP) closeConns() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
	f.conns = nil
}

func (f *fakeCDP) calls() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCall(nil), f.methods...)
}

func (f *fakeCDP) serve(conn *websocket.Conn) {
	var writeMu sync.Mutex
	send := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.WriteJSON(v)
	}
	for {
		var req struct {
			ID        int64          `json:"id"`
			Method    string         `json:"method"`
			Params    map[string]any `json:"params"`
			SessionID string         `json:"sessionId"`
		}
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		f.mu.Lock()
		f.methods = append(f.methods, recordedCall{Method: req.Method, Params: req.Params, SessionID: req.SessionID})
		f.mu.Unlock()

		reply := func(result any) { send(map[string]any{"id": req.ID, "result": result}) }
		switch req.Method {
		case "Target.createTarget":
			go func(id int64) {
				time.Sleep(f.createDelay)
				send(map[string]any{"id": id, "result": map[string]any{"targetId": "T1"}})
			}(req.ID)
		case "Target.attachToTarget":
			send(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S1"}})
			send(map[string]any{"id": req.ID + 1000, "result": map[string]any{}}) // stray reply for an unknown id
			reply(map[string]any{"sessionId": "S1"})
		case "Target.closeTarget":
			reply(map[string]any{"success": true})
		case "Runtime.evaluate":
			if f.evalCDPErrors.Load() > 0 {
				f.evalCDPErrors.Add(-1)
				send(map[string]any{"id": req.ID, "error": map[string]any{"code": -32000, "message": "Cannot find default execution context"}})
				continue
			}
			// Answer in the background so a slow evalFn does not block other calls.
			go func(id int64, expr string) {
				var value any
				var exception string
				if f.evalFn != nil {
					value, exception = f.evalFn(expr)
				}
				if exception != "" {
					send(map[string]any{"id": id, "result": map[string]any{
						"result":           map[string]any{"type": "object", "subtype": "error"},
						"exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": exception}},
					}})
					return
				}
				raw, _ := json.Marshal(value)
				send(map[string]any{"id": id, "result": map[string]any{"result": map[string]any{"type": "object", "value": json.RawMessage(raw)}}})
			}(req.ID, fmt.Sprint(req.Params["expression"]))
		default:
			send(map[string]any{"id": req.ID, "error": map[string]any{"code": -32601, "message": fmt.Sprintf("'%s' wasn't found", req.Method)}})
		}
	}
}
