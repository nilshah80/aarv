package aarv

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodecAndResponseAdditionalCoverage(t *testing.T) {
	t.Run("codec helpers", func(t *testing.T) {
		var decoded map[string]string
		if err := (StdJSONCodec{}).Decode(bytes.NewBufferString(`{"hello":"world"}`), &decoded); err != nil {
			t.Fatalf("unexpected std decode error: %v", err)
		}
		if data, err := (StdJSONCodec{}).MarshalBytes(map[string]string{"k": "v"}); err != nil || !bytes.Contains(data, []byte(`"k":"v"`)) {
			t.Fatalf("unexpected std marshal result: %q err=%v", data, err)
		}

		codec := NewOptimizedJSONCodec()
		if codec.ContentType() != "application/json" {
			t.Fatal("unexpected optimized codec content type")
		}
		var out bytes.Buffer
		if err := codec.Encode(&out, map[string]string{"x": "y"}); err != nil {
			t.Fatalf("unexpected optimized encode error: %v", err)
		}
		if !bytes.Contains(out.Bytes(), []byte(`"x":"y"`)) {
			t.Fatalf("unexpected optimized encode output: %q", out.Bytes())
		}
		if err := codec.Decode(bytes.NewBufferString(`{"x":"y"}`), &decoded); err != nil {
			t.Fatalf("unexpected optimized decode error: %v", err)
		}
		if err := codec.UnmarshalBytes([]byte(`{"x":"y"}`), &decoded); err != nil {
			t.Fatalf("unexpected optimized unmarshal error: %v", err)
		}
		if data, err := codec.MarshalBytes(map[string]string{"x": "y"}); err != nil || !bytes.Contains(data, []byte(`"x":"y"`)) {
			t.Fatalf("unexpected optimized marshal result: %q err=%v", data, err)
		}
		if err := codec.Encode(io.Discard, failingJSONValue{}); err == nil {
			t.Fatal("expected optimized encode failure")
		}
		if _, err := codec.MarshalBytes(failingJSONValue{}); err == nil {
			t.Fatal("expected optimized marshal failure")
		}
	})

	t.Run("buffered writer helpers", func(t *testing.T) {
		base := &featureResponseWriter{}
		bw := acquireBufferedWriter(base)
		defer releaseBufferedWriter(bw)

		bw.WriteHeader(http.StatusAccepted)
		bw.WriteHeader(http.StatusCreated)
		if _, err := bw.Write([]byte("hello")); err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
		bw.flush()
		if base.status != http.StatusAccepted {
			t.Fatalf("unexpected flushed status: %d", base.status)
		}

		bw = acquireBufferedWriter(base)
		if _, err := bw.Write([]byte("pre")); err != nil {
			t.Fatalf("unexpected pre-bypass write error: %v", err)
		}
		bw.Bypass()
		if base.body.String() == "" {
			t.Fatal("expected bypass to flush buffered body")
		}
		if _, err := bw.Write([]byte("direct")); err != nil {
			t.Fatalf("unexpected bypass write error: %v", err)
		}
		bw.WriteHeader(http.StatusNoContent)
		bw.Flush()
		if base.flushed == 0 {
			t.Fatal("expected underlying flusher to be called")
		}
		bw.flush()
		if _, _, err := bw.Hijack(); err != nil || !base.hijacked {
			t.Fatalf("unexpected hijack result: %v", err)
		}
		if err := bw.Push("/asset.js", nil); err != nil || len(base.pushes) != 1 {
			t.Fatalf("unexpected push result: err=%v pushes=%v", err, base.pushes)
		}
		if itoa(0) != "0" || itoa(-12) != "-12" || itoa(34) != "34" {
			t.Fatal("unexpected itoa output")
		}

	})
}

func TestResponseWrapper(t *testing.T) {
	w := httptest.NewRecorder()
	res := acquireBufferedWriter(w)
	defer releaseBufferedWriter(res)

	if res.StatusCode() != http.StatusOK {
		t.Errorf("Expected default status 200, got %d", res.StatusCode())
	}

	res.WriteHeader(http.StatusCreated)
	if res.StatusCode() != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", res.StatusCode())
	}

	n, err := res.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Errorf("Expected 5 bytes written, got %d, err %v", n, err)
	}

	if !bytes.Equal(res.Body(), []byte("hello")) {
		t.Errorf("Expected body to be 'hello', got %s", string(res.Body()))
	}

	res.SetBody([]byte("world"))
	if !bytes.Equal(res.Body(), []byte("world")) {
		t.Errorf("Expected body to be 'world', got %s", string(res.Body()))
	}

	if len(res.Body()) != 5 {
		t.Errorf("Expected size 5, got %d", len(res.Body()))
	}

	if !res.written {
		t.Errorf("Expected written to be true")
	}

	res.Flush() // check safe flushing
	if w.Code != http.StatusCreated {
		t.Errorf("Expected flushed recorder code to match")
	}
	if got := w.Header().Get("Content-Length"); got != "5" {
		t.Errorf("Expected Content-Length 5, got %q", got)
	}

	res.Bypass()
	if !res.bypassed {
		t.Errorf("Expected bypassed to be true")
	}
}

func TestResponseUnwrap(t *testing.T) {
	w := httptest.NewRecorder()
	res := acquireBufferedWriter(w)
	defer releaseBufferedWriter(res)

	unwrapped := res.Unwrap()
	if unwrapped != w {
		t.Errorf("Expected unwrap to return underlying recorder")
	}

	http2Ok := res.Push("test", nil)
	if http2Ok == nil {
		t.Errorf("Expected push to return err on httptest context")
	}

	_, _, hijackOk := res.Hijack()
	if hijackOk == nil {
		t.Errorf("Expected hijack to return err on httptest context")
	}
}
