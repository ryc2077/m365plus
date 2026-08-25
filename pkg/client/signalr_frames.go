package client

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxSignalRBufferedBytes = 8 << 20

// signalRFrameDecoder preserves an unterminated SignalR record across
// WebSocket messages. ChatHub normally aligns records with WebSocket frames,
// but proxies and upstream changes may split a JSON record at any byte.
type signalRFrameDecoder struct {
	buffer string
}

func (decoder *signalRFrameDecoder) Feed(chunk []byte) ([]map[string]any, error) {
	decoder.buffer += string(chunk)
	if len(decoder.buffer) > maxSignalRBufferedBytes {
		decoder.buffer = ""
		return nil, fmt.Errorf("SignalR frame buffer exceeds %d bytes", maxSignalRBufferedBytes)
	}

	var frames []map[string]any
	for {
		index := strings.Index(decoder.buffer, signalRDelimiter)
		if index < 0 {
			break
		}
		record := strings.TrimSpace(decoder.buffer[:index])
		decoder.buffer = decoder.buffer[index+len(signalRDelimiter):]
		if record == "" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(record), &frame); err != nil {
			return nil, fmt.Errorf("decode SignalR frame: %w", err)
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

func (decoder *signalRFrameDecoder) Empty() bool {
	return strings.TrimSpace(decoder.buffer) == ""
}
