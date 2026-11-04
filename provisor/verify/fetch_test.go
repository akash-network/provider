package verify

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchTransferFailedOnTruncatedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("short body"))
	}))
	defer server.Close()

	_, reason := fetch(context.Background(), server.Client(), server.URL, time.Now)
	if reason != ReasonTransferFailed {
		t.Fatalf("fetch() reason = %q, want %q", reason, ReasonTransferFailed)
	}
}

func TestFetchTransferTooLargeOnOversizedBody(t *testing.T) {
	oversized := bytes.Repeat([]byte("a"), maxResponseBytes+4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(oversized)
	}))
	defer server.Close()

	_, reason := fetch(context.Background(), server.Client(), server.URL, time.Now)
	if reason != ReasonTransferTooLarge {
		t.Fatalf("fetch() reason = %q, want %q", reason, ReasonTransferTooLarge)
	}
}

func TestFetchTransferTooSlowOnStalledWriter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(400 * time.Millisecond)
		w.Write([]byte("y"))
	}))
	defer server.Close()

	_, reason := fetch(context.Background(), server.Client(), server.URL, time.Now)
	if reason != ReasonTransferTooSlow {
		t.Fatalf("fetch() reason = %q, want %q", reason, ReasonTransferTooSlow)
	}
}

func TestFetchClockSkewOnImplausibleDateHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", time.Now().Add(-48*time.Hour).Format(http.TimeFormat))
		w.Write([]byte("body"))
	}))
	defer server.Close()

	_, reason := fetch(context.Background(), server.Client(), server.URL, time.Now)
	if reason != ReasonClockSkew {
		t.Fatalf("fetch() reason = %q, want %q", reason, ReasonClockSkew)
	}
}

func TestFetchSucceedsWithinEveryLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ordinary body"))
	}))
	defer server.Close()

	body, reason := fetch(context.Background(), server.Client(), server.URL, time.Now)
	if reason != "" {
		t.Fatalf("fetch() reason = %q, want empty", reason)
	}
	if string(body) != "ordinary body" {
		t.Fatalf("fetch() body = %q, want %q", body, "ordinary body")
	}
}
