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
	// Create a mock channel
	channels["live"] = &Channel{}

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

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	expectedContentType := "application/vnd.apple.mpegurl"
	if ct := rr.Header().Get("Content-Type"); ct != expectedContentType {
		t.Errorf("handler returned wrong content type: got %v want %v",
			ct, expectedContentType)
	}

	body := rr.Body.String()
	t.Logf("Generated playlist:\n%s", body)
	
	if !strings.Contains(body, "#EXTM3U") {
		t.Errorf("playlist should contain #EXTM3U header")
	}

	if !strings.Contains(body, "#EXT-X-VERSION:3") {
		t.Errorf("playlist should contain version header")
	}

	// The playlist might start empty and build segments over time
	// So we just check that it's a valid HLS playlist structure
	if !strings.Contains(body, "#EXT-X-TARGETDURATION") {
		t.Errorf("playlist should contain target duration")
	}
}