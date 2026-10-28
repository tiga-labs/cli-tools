package chromeattach

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserDataDir(t *testing.T) {
	tests := []struct {
		goos, want string
		wantErr    bool
	}{
		{"darwin", "/h/Library/Application Support/Google/Chrome", false},
		{"linux", "/h/.config/google-chrome", false},
		{"windows", "", true},
	}
	for _, tt := range tests {
		got, err := userDataDir(tt.goos, "/h")
		if (err != nil) != tt.wantErr {
			t.Errorf("userDataDir(%q) error = %v, wantErr %v", tt.goos, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("userDataDir(%q) = %q, want %q", tt.goos, got, tt.want)
		}
	}
}

func TestReadEndpoint(t *testing.T) {
	tests := []struct {
		name, content, want string
		wantErr             bool
	}{
		{"lf", "65449\n/devtools/browser/abc\n", "ws://127.0.0.1:65449/devtools/browser/abc", false},
		{"crlf", "65449\r\n/devtools/browser/abc\r\n", "ws://127.0.0.1:65449/devtools/browser/abc", false},
		{"one line", "65449", "", true},
		{"bad port", "x\n/p", "", true},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(tt.content), 0o600)
		got, err := readEndpoint(dir)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: error = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if err != nil && errors.Is(err, ErrDebuggingOff) {
			t.Errorf("%s: malformed file reported as ErrDebuggingOff", tt.name)
		}
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}

	if _, err := readEndpoint(t.TempDir()); !errors.Is(err, ErrDebuggingOff) {
		t.Errorf("missing file: err = %v, want ErrDebuggingOff", err)
	}
}

func TestConnectNoPortFile(t *testing.T) {
	_, err := Connect(context.Background(), Options{UserDataDir: t.TempDir()})
	if !errors.Is(err, ErrDebuggingOff) {
		t.Fatalf("err = %v, want ErrDebuggingOff", err)
	}
}

// deadPort returns a localhost port with nothing listening on it.
func deadPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	return port
}

func TestConnectRefused(t *testing.T) {
	dir := t.TempDir()
	writePortFile(t, dir, deadPort(t), fakeBrowserPath)
	_, err := Connect(context.Background(), Options{UserDataDir: dir, RetryDelay: 10 * time.Millisecond})
	if !errors.Is(err, ErrChromeUnreachable) {
		t.Fatalf("err = %v, want ErrChromeUnreachable", err)
	}
}

func TestConnectStaleFileRewritten(t *testing.T) {
	f := newFakeCDP(t)
	dir := t.TempDir()
	writePortFile(t, dir, deadPort(t), fakeBrowserPath)
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.writePortFile(dir)
	}()
	b, err := Connect(context.Background(), Options{UserDataDir: dir, RetryDelay: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("Connect after rewrite: %v", err)
	}
	b.Close()
}

func TestConnectDenied(t *testing.T) {
	f := newFakeCDP(t)
	f.deny = true
	dir := t.TempDir()
	f.writePortFile(dir)
	_, err := Connect(context.Background(), Options{UserDataDir: dir})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
}

func TestConnectApprovalTimeout(t *testing.T) {
	f := newFakeCDP(t)
	f.handshakeDelay = 2 * time.Second
	dir := t.TempDir()
	f.writePortFile(dir)
	start := time.Now()
	_, err := Connect(context.Background(), Options{UserDataDir: dir, ApproveTimeout: 100 * time.Millisecond})
	if !errors.Is(err, ErrApprovalTimeout) {
		t.Fatalf("err = %v, want ErrApprovalTimeout", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %s, want < 1s", d)
	}
}

func TestConnectPrintsPrompt(t *testing.T) {
	f := newFakeCDP(t)
	dir := t.TempDir()
	f.writePortFile(dir)
	var buf bytes.Buffer
	b, err := Connect(context.Background(), Options{UserDataDir: dir, Stderr: &buf})
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if !strings.Contains(buf.String(), `Approve the "Allow remote debugging?" prompt in Chrome…`) {
		t.Fatalf("stderr = %q, want approve prompt", buf.String())
	}
}

func TestErrorCopyLeadsMessage(t *testing.T) {
	dir := t.TempDir()
	_, err := Connect(context.Background(), Options{UserDataDir: dir})
	if !strings.HasPrefix(err.Error(), "Chrome remote debugging is off.") {
		t.Fatalf("err = %q, want the ErrDebuggingOff copy first", err)
	}
}

func TestConnectCancelDuringApproval(t *testing.T) {
	f := newFakeCDP(t)
	f.handshakeDelay = 2 * time.Second
	dir := t.TempDir()
	f.writePortFile(dir)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := Connect(ctx, Options{UserDataDir: dir, ApproveTimeout: 5 * time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Ctrl-C during the approval wait took %s, want < 500ms", d)
	}
}

func TestReadEndpointEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEndpoint(dir); err == nil || errors.Is(err, ErrDebuggingOff) {
		t.Fatalf("err = %v, want a malformed-file error", err)
	}
}

func TestConnectHalfWrittenFileRetried(t *testing.T) {
	f := newFakeCDP(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("65"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.writePortFile(dir)
	}()
	b, err := Connect(context.Background(), Options{UserDataDir: dir, RetryDelay: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("Connect after the file was completed: %v", err)
	}
	b.Close()
}

func TestConnectMalformedFileIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("65"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Connect(context.Background(), Options{UserDataDir: dir, RetryDelay: 10 * time.Millisecond})
	if !errors.Is(err, ErrChromeUnreachable) {
		t.Fatalf("err = %v, want ErrChromeUnreachable", err)
	}
}
