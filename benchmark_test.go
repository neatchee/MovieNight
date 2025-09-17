package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Benchmark the simplified HLS playlist generation
func BenchmarkHLSPlaylistHandler(b *testing.B) {
	// Create a mock stream to avoid 404s during benchmarking
	channels["live"] = &Channel{
		que: nil, // This will return 404, but we're measuring handler performance
	}
	defer func() {
		delete(channels, "live")
	}()

	req, _ := http.NewRequest("GET", "/live.m3u8", nil)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handleHLSPlaylist)
		handler.ServeHTTP(rr, req)
	}
}

// Benchmark the simplified HLS stream handler
func BenchmarkHLSStreamHandler(b *testing.B) {
	// Create a mock stream to avoid 404s during benchmarking
	channels["live"] = &Channel{
		que: nil, // This will return 404, but we're measuring handler performance
	}
	defer func() {
		delete(channels, "live")
	}()

	req, _ := http.NewRequest("GET", "/live.ts", nil)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handleHLSStream)
		handler.ServeHTTP(rr, req)
	}
}

// Benchmark writeFlusher panic recovery overhead
func BenchmarkWriteFlusherWrite(b *testing.B) {
	// Mock flusher for testing
	mockFlusher := &mockHttpFlusher{}
	mockWriter := &mockWriter{}
	
	wf := writeFlusher{
		httpflusher: mockFlusher,
		Writer:      mockWriter,
	}
	
	data := []byte("test data for writing")
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wf.Write(data)
	}
}

// Mock implementations for benchmarking
type mockWriter struct{}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

type mockHttpFlusher struct{}

func (m *mockHttpFlusher) Flush() {}