package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestAppServerStartContextGracefulShutdown(t *testing.T) {
	server := NewAppServer(nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.StartContext(ctx, "127.0.0.1:0", func(addr net.Addr) {
			addrCh <- addr.String()
		})
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case err := <-errCh:
		t.Fatalf("StartContext exited before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for StartContext listener")
	}

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartContext returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for StartContext graceful shutdown")
	}
}

func TestWatchReaderEOFCancelsOnPipeClose(t *testing.T) {
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watchReaderEOF(pr, cancel)

	if _, err := pw.Write([]byte("keepalive\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("context canceled before reader EOF")
	case <-time.After(20 * time.Millisecond):
	}

	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for context cancellation on EOF")
	}
}
