package chromeattach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const approvePrompt = `Approve the "Allow remote debugging?" prompt in Chrome…`

// Options configures Connect. The zero value attaches to the default Chrome install.
type Options struct {
	// UserDataDir is Chrome's user data directory; empty means DefaultUserDataDir.
	UserDataDir string
	// ApproveTimeout bounds the wait for the user to answer Chrome's prompt (default 2m).
	ApproveTimeout time.Duration
	// RetryDelay is the wait before re-reading a stale DevToolsActivePort (default 2s).
	RetryDelay time.Duration
	// Stderr receives the approve prompt line; nil discards it.
	Stderr io.Writer
}

// DefaultUserDataDir returns Google Chrome's user data directory for this OS.
func DefaultUserDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return userDataDir(runtime.GOOS, home)
}

func userDataDir(goos, home string) (string, error) {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome"), nil
	case "linux":
		return filepath.Join(home, ".config", "google-chrome"), nil
	}
	return "", fmt.Errorf("unsupported OS %q", goos)
}

// errMalformedPortFile marks a DevToolsActivePort Chrome has not finished writing.
var errMalformedPortFile = errors.New("unexpected DevToolsActivePort contents")

// readEndpoint turns Chrome's DevToolsActivePort file (port, then browser path) into a
// WebSocket URL. A missing file means remote debugging is off.
func readEndpoint(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrDebuggingOff
	}
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n")), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("%w (%d lines)", errMalformedPortFile, len(lines))
	}
	port, path := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("%w (port %q)", errMalformedPortFile, port)
	}
	return "ws://127.0.0.1:" + port + path, nil
}

// Connect attaches to the running Chrome. Chrome asks the user to approve every
// connection, so this blocks until they click Allow or Deny, or ApproveTimeout passes.
func Connect(ctx context.Context, opts Options) (*Browser, error) {
	if opts.ApproveTimeout <= 0 {
		opts.ApproveTimeout = 2 * time.Minute
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = 2 * time.Second
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	dir := opts.UserDataDir
	if dir == "" {
		var err error
		if dir, err = DefaultUserDataDir(); err != nil {
			return nil, err
		}
	}

	attempt := func() (*websocket.Conn, error) {
		wsURL, err := readEndpoint(dir)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(opts.Stderr, approvePrompt)
		return dial(ctx, wsURL, opts.ApproveTimeout)
	}
	// Right after a restart Chrome can leave the previous port in the file, or be midway
	// through rewriting it. Wait once and read it again.
	stale := func(err error) bool {
		return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, errMalformedPortFile)
	}
	conn, err := attempt()
	if stale(err) {
		select {
		case <-time.After(opts.RetryDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		conn, err = attempt()
		if stale(err) {
			return nil, fmt.Errorf("%w (%v)", ErrChromeUnreachable, err)
		}
	}
	if err != nil {
		return nil, err
	}
	return newBrowser(conn), nil
}

func dial(ctx context.Context, wsURL string, approveTimeout time.Duration) (*websocket.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, approveTimeout)
	defer cancel()
	// gorilla/websocket only applies the context's deadline to the handshake, not its
	// cancellation, so unblock the handshake ourselves when dctx ends (Ctrl-C).
	finished := make(chan struct{})
	defer close(finished)
	// No HandshakeTimeout: the handshake stays open while the user reads Chrome's prompt.
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			go func() {
				select {
				case <-dctx.Done():
					c.SetDeadline(time.Now())
				case <-finished:
				}
			}()
			return c, nil
		},
	}
	conn, resp, err := dialer.DialContext(dctx, wsURL, nil)
	if err == nil {
		return conn, nil
	}
	if resp != nil && resp.StatusCode == http.StatusForbidden {
		return nil, ErrDenied
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return nil, ErrApprovalTimeout
	}
	return nil, err
}
