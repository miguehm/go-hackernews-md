package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hnmd/internal/config"
	"hnmd/internal/store"
)

func envFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

// fakeRefresher records its lifecycle without touching the network.
type fakeRefresher struct {
	started chan struct{}
	stopped chan struct{}
}

func newFakeRefresher() *fakeRefresher {
	return &fakeRefresher{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (f *fakeRefresher) StartRefresher(ctx context.Context) <-chan struct{} {
	close(f.started)
	go func() {
		<-ctx.Done()
		close(f.stopped)
	}()
	return f.stopped
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestRunInvalidConfig(t *testing.T) {
	err := run(context.Background(), envFrom(map[string]string{"TOP_N": "0", "SCRAPE_TIMEOUT": "abc"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "TOP_N") || !strings.Contains(err.Error(), "SCRAPE_TIMEOUT") {
		t.Fatalf("run = %v, want joined config errors", err)
	}
}

func TestRunListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	err = run(context.Background(), envFrom(map[string]string{"PORT": port, "DATA_DIR": t.TempDir()}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("run = %v, want listen error", err)
	}
}

func startServe(t *testing.T, env map[string]string) (base string, ref *fakeRefresher, logs *syncBuffer, stop func() error) {
	t.Helper()
	if env["DATA_DIR"] == "" {
		env["DATA_DIR"] = t.TempDir()
	}
	cfg, err := config.Load(envFrom(env))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logs = &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	ref = newFakeRefresher()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, logger, ln, store.New(), ref) }()
	<-ref.started

	stop = func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(3 * time.Second):
			t.Fatal("serve did not return after cancel")
			return nil
		}
	}
	return "http://" + ln.Addr().String(), ref, logs, stop
}

func do(t *testing.T, method, url string, headers map[string]string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

func TestServeWiresRoutesAuthAndShutdown(t *testing.T) {
	base, ref, logs, stop := startServe(t, map[string]string{"API_KEYS": "secret"})
	auth := map[string]string{"Authorization": "Bearer secret"}

	if resp := do(t, http.MethodGet, base+"/health", nil, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("/health = %d, want 200 without key", resp.StatusCode)
	}
	if resp := do(t, http.MethodGet, base+"/v1/status", nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/v1/status without key = %d, want 401", resp.StatusCode)
	}
	resp := do(t, http.MethodGet, base+"/v1/status", auth, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Request-ID") == "" {
		t.Fatalf("/v1/status = %d, X-Request-ID %q", resp.StatusCode, resp.Header.Get("X-Request-ID"))
	}
	if resp := do(t, http.MethodGet, base+"/v1/top", auth, ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/v1/top before refresh = %d, want 503", resp.StatusCode)
	}
	preflight := map[string]string{"Origin": "http://localhost:5173", "Access-Control-Request-Method": "PATCH"}
	if resp := do(t, http.MethodOptions, base+"/v1/todos/tasks/x", preflight, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight = %d, want 204 without key", resp.StatusCode)
	}

	create := map[string]string{"Authorization": "Bearer secret", "Content-Type": "application/json"}
	resp = do(t, http.MethodPost, base+"/v1/todos/tasks", create, `{"text":"e2e #smoke"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	patch := map[string]string{"Authorization": "Bearer secret", "Content-Type": "application/json", "If-Match": resp.Header.Get("ETag")}
	if resp := do(t, http.MethodPatch, base+resp.Header.Get("Location"), patch, `{"done":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("patch with returned ETag = %d", resp.StatusCode)
	}

	if err := stop(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	select {
	case <-ref.stopped:
	default:
		t.Fatal("refresher was not stopped")
	}
	out := logs.String()
	for _, want := range []string{`"msg":"listening"`, `"route":"/v1/todos/tasks/{id}"`, `"msg":"shutting down"`} {
		if !strings.Contains(out, want) {
			t.Errorf("logs missing %s:\n%s", want, out)
		}
	}
}

func TestServeProtectHealthAndInsecureWrites(t *testing.T) {
	base, _, _, stop := startServe(t, map[string]string{"API_KEYS": "secret", "PROTECT_HEALTH": "true"})
	if resp := do(t, http.MethodGet, base+"/health", nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/health = %d, want 401 with PROTECT_HEALTH", resp.StatusCode)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	base, _, _, stop = startServe(t, map[string]string{})
	json := map[string]string{"Content-Type": "application/json"}
	if resp := do(t, http.MethodPost, base+"/v1/todos/tasks", json, `{"text":"x"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("write without auth = %d, want 403", resp.StatusCode)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	base, _, _, stop = startServe(t, map[string]string{"TODO_ALLOW_INSECURE_WRITES": "true"})
	if resp := do(t, http.MethodPost, base+"/v1/todos/tasks", json, `{"text":"x"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("insecure write = %d, want 201", resp.StatusCode)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestServeTodoStoreError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "todos.json")
	if err := os.WriteFile(file, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(envFrom(map[string]string{"TODO_FILE": file}))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	err = serve(context.Background(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), ln, store.New(), newFakeRefresher())
	if err == nil || !strings.Contains(err.Error(), "todo store") {
		t.Fatalf("serve = %v, want todo store error", err)
	}
	if _, err := ln.Accept(); err == nil {
		t.Fatal("listener should be closed on early failure")
	}
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, slog.LevelWarn, "json").Info("hidden")
	newLogger(&buf, slog.LevelWarn, "json").Warn("shown", "k", "v")
	if out := buf.String(); strings.Contains(out, "hidden") || !strings.Contains(out, `"msg":"shown"`) {
		t.Fatalf("json output = %q", out)
	}
	buf.Reset()
	newLogger(&buf, slog.LevelInfo, "text").Info("hello")
	if !strings.Contains(buf.String(), "msg=hello") {
		t.Fatalf("text output = %q", buf.String())
	}
}

func TestBuildVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "1.2.3"
	if got := buildVersion(); got != "1.2.3" {
		t.Fatalf("buildVersion = %q", got)
	}
	version = "dev"
	if got := buildVersion(); got == "" {
		t.Fatal("buildVersion must never be empty")
	}
}

func TestRunStartsAndStops(t *testing.T) {
	// A canceled context makes run wire everything, start and immediately
	// shut down; the refresh fails on the canceled context without dialing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logs := &syncBuffer{}
	err := run(ctx, envFrom(map[string]string{"PORT": "0", "DATA_DIR": t.TempDir(), "LOG_LEVEL": "debug", "PROTECT_HEALTH": "maybe"}), logs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := logs.String()
	for _, want := range []string{`"msg":"configuration"`, `"msg":"listening"`, `"msg":"shutting down"`} {
		if !strings.Contains(out, want) {
			t.Errorf("logs missing %s:\n%s", want, out)
		}
	}
}
