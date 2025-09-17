package main

import (
	"net/http"
	"net/http/httptest"
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
	// For testing, we'll just verify the nil pointer fix works
	// by ensuring the handler doesn't crash with nil channels
	
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

func TestHLSSegmentWithNilQueue(t *testing.T) {
	// Test that segment handler doesn't crash with nil queue
	channels["live"] = &Channel{
		que: nil, // Nil queue should be handled gracefully
	}

	defer func() {
		delete(channels, "live")
	}()

	req, err := http.NewRequest("GET", "/live_segment_0.ts", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSSegment)
	handler.ServeHTTP(rr, req)

	// Should return 404 due to nil queue, not crash
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusNotFound)
	}
}