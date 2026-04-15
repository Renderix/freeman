package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_Transcribe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":" hello world "}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	text, err := c.Transcribe(context.Background(), EncodeWAV([]int16{0, 1, 2, 3}, 16000))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if strings.TrimSpace(text) != "hello world" {
		t.Errorf("text = %q, want hello world", text)
	}
}

func TestClient_Transcribe_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	_, err := c.Transcribe(context.Background(), EncodeWAV([]int16{0}, 16000))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_Transcribe_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	text, err := c.Transcribe(context.Background(), EncodeWAV([]int16{0}, 16000))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}
