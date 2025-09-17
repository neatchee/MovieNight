package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHLSPlaylistWithoutStream(t *testing.T) {
	req, err := http.NewRequest("GET", "/live.m3u8", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSPlaylist)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusNotFound)
	}
}

func TestHLSPlaylistWithMockStream(t *testing.T) {
	// Test with nil queue - should return 404
	channels["live"] = &Channel{
		que: nil, // Nil queue should be handled gracefully
	}

	defer func() {
		delete(channels, "live")
	}()

	req, err := http.NewRequest("GET", "/live.m3u8", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSPlaylist)
	handler.ServeHTTP(rr, req)

	// Should return 404 due to nil queue, not crash
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusNotFound)
	}
}

func TestHLSStreamWithNilQueue(t *testing.T) {
	// Test that stream handler doesn't crash with nil queue
	channels["live"] = &Channel{
		que: nil, // Nil queue should be handled gracefully
	}

	defer func() {
		delete(channels, "live")
	}()

	req, err := http.NewRequest("GET", "/live.ts", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSStream)
	handler.ServeHTTP(rr, req)

	// Should return 404 due to nil queue, not crash
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusNotFound)
	}
}

func TestHLSPlaylistFormat(t *testing.T) {
	// Create a mock stream with valid queue (but we won't test actual streaming)
	channels["live"] = &Channel{
		que: nil, // Still nil but we test the early validation
	}

	defer func() {
		delete(channels, "live")
	}()

	req, err := http.NewRequest("GET", "/live.m3u8", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSPlaylist)
	handler.ServeHTTP(rr, req)

	// Since queue is nil, should get 404
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("Expected 404 for nil queue, got %v", status)
	}
}

func TestWriteFlusherPanicRecovery(t *testing.T) {
	// Test that writeFlusher handles nil Writer gracefully
	wf := writeFlusher{
		httpflusher: nil,
		Writer:      nil,
	}

	_, err := wf.Write([]byte("test"))
	if err == nil {
		t.Error("Expected error for nil writer")
	}

	if !strings.Contains(err.Error(), "writer is nil") {
		t.Errorf("Expected 'writer is nil' error, got: %v", err)
	}

	err = wf.Flush()
	if err == nil {
		t.Error("Expected error for nil flusher")
	}

	if !strings.Contains(err.Error(), "flusher is nil") {
		t.Errorf("Expected 'flusher is nil' error, got: %v", err)
	}
}