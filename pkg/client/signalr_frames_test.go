package client

import "testing"

func TestSignalRFrameDecoderPreservesSplitRecord(t *testing.T) {
	var decoder signalRFrameDecoder
	frames, err := decoder.Feed([]byte(`{"type":1,"target":"up`))
	if err != nil || len(frames) != 0 {
		t.Fatalf("first feed: frames=%#v err=%v", frames, err)
	}
	frames, err = decoder.Feed([]byte(`date"}` + signalRDelimiter + `{"type":3}` + signalRDelimiter))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0]["target"] != "update" || int(frames[1]["type"].(float64)) != 3 {
		t.Fatalf("frames = %#v", frames)
	}
	if !decoder.Empty() {
		t.Fatal("decoder retained bytes after complete records")
	}
}

func TestSignalRFrameDecoderRejectsMalformedCompleteRecord(t *testing.T) {
	var decoder signalRFrameDecoder
	if _, err := decoder.Feed([]byte(`{not-json}` + signalRDelimiter)); err == nil {
		t.Fatal("malformed complete record was accepted")
	}
}
