package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
)

func makeSSEServer(lines []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(":ok\n\n"))
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		for _, line := range lines {
			w.Write([]byte(line + "\n"))
			flusher.Flush()
		}
	}))
}

func BenchmarkReadStreamSimple(b *testing.B) {
	srv := makeSSEServer([]string{
		`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
	})
	defer srv.Close()

	c := &client{baseURL: srv.URL, apiKey: "test", name: "bench"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/chat/completions", nil)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		out := make(chan provider.Chunk, 256)
		go c.readStream(context.Background(), resp, out)
		for range out {
		}
	}
}

func BenchmarkReadStreamManyChunks(b *testing.B) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, `data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`)
	}
	lines = append(lines, "data: [DONE]")

	srv := makeSSEServer(lines)
	defer srv.Close()

	c := &client{baseURL: srv.URL, apiKey: "test", name: "bench"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/chat/completions", nil)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		out := make(chan provider.Chunk, 256)
		go c.readStream(context.Background(), resp, out)
		for range out {
		}
	}
}
