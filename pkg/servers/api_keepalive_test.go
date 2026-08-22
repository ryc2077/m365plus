package servers

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ryc2077/m365plus/pkg/client"
)

func TestKeepaliveFramesMatchWireFormat(t *testing.T) {
	recorder := httptest.NewRecorder()
	if err := writeSSEKeepalive(recorder, recorder); err != nil {
		t.Fatalf("OpenAI keepalive failed: %v", err)
	}
	if got := recorder.Body.String(); got != ": keepalive\n\n" {
		t.Fatalf("OpenAI keepalive = %q", got)
	}

	recorder = httptest.NewRecorder()
	if err := writeAnthropicKeepalive(recorder, recorder); err != nil {
		t.Fatalf("Anthropic keepalive failed: %v", err)
	}
	if got := recorder.Body.String(); got != "event: ping\ndata: {\"type\":\"ping\"}\n\n" {
		t.Fatalf("Anthropic keepalive = %q", got)
	}
}

func TestNextStreamChunkWritesWhileUpstreamIsSilent(t *testing.T) {
	chunks := make(chan client.StreamChunk, 1)
	keepalive := time.NewTicker(5 * time.Millisecond)
	defer keepalive.Stop()

	writes := make(chan struct{}, 16)
	go func() {
		time.Sleep(30 * time.Millisecond)
		chunks <- client.StreamChunk{Text: "hello"}
	}()

	chunk, more := nextStreamChunk(context.Background(), chunks, keepalive, httptest.NewRecorder(), func() error {
		writes <- struct{}{}
		return nil
	})
	if !more || chunk.Text != "hello" {
		t.Fatalf("chunk = %#v, more = %v", chunk, more)
	}
	if len(writes) == 0 {
		t.Fatal("no keepalive written during silent wait")
	}
}

func TestNextStreamChunkStopsAfterKeepaliveFailure(t *testing.T) {
	chunks := make(chan client.StreamChunk)
	keepalive := time.NewTicker(time.Millisecond)
	defer keepalive.Stop()

	if _, more := nextStreamChunk(context.Background(), chunks, keepalive, httptest.NewRecorder(), func() error {
		return errors.New("connection reset")
	}); more {
		t.Fatal("failed keepalive did not end stream")
	}
}

func TestNextStreamChunkStopsOnCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	chunks := make(chan client.StreamChunk)
	keepalive := time.NewTicker(time.Hour)
	defer keepalive.Stop()

	if _, more := nextStreamChunk(ctx, chunks, keepalive, httptest.NewRecorder(), func() error {
		t.Fatal("canceled request wrote a keepalive")
		return nil
	}); more {
		t.Fatal("canceled request reported another chunk")
	}
}
