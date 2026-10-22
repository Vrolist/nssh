package base_tunnel

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
	"math/rand"
	"testing"
)

// decodeFrames 解析 compressWriter 产生的帧流，返回原始字节与压缩/原始帧数量。
func decodeFrames(t *testing.T, data []byte) ([]byte, int, int) {
	t.Helper()
	var out []byte
	compressedFrames, rawFrames := 0, 0
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		var hdr [compressFrameHeaderSize]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			t.Fatalf("read frame header: %v", err)
		}
		length := int(binary.BigEndian.Uint32(hdr[0:4]))
		if length <= 0 {
			t.Fatalf("invalid frame length: %d", length)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			t.Fatalf("read frame payload: %v", err)
		}
		if hdr[4]&compressFrameFlagCompressed != 0 {
			fr := flate.NewReader(bytes.NewReader(payload))
			decoded, err := io.ReadAll(fr)
			_ = fr.Close()
			if err != nil {
				t.Fatalf("decompress frame: %v", err)
			}
			out = append(out, decoded...)
			compressedFrames++
		} else {
			out = append(out, payload...)
			rawFrames++
		}
	}
	return out, compressedFrames, rawFrames
}

func TestCompressWriterRoundTripText(t *testing.T) {
	// 300KB 可压缩文本，跨多个 64KB 块
	input := bytes.Repeat([]byte("hello nwy tunnel compression! "), 12000)

	var buf bytes.Buffer
	w := newCompressWriter(&buf)
	n, err := w.Write(input)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(input) {
		t.Fatalf("short write: got %d want %d", n, len(input))
	}

	out, compressedFrames, rawFrames := decodeFrames(t, buf.Bytes())
	if !bytes.Equal(out, input) {
		t.Fatalf("round trip mismatch: got %d bytes want %d", len(out), len(input))
	}
	if compressedFrames == 0 {
		t.Fatal("expected compressed frames for compressible text")
	}
	if rawFrames != 0 {
		t.Fatalf("expected no raw frames, got %d", rawFrames)
	}
	if buf.Len() >= len(input) {
		t.Fatalf("expected compressed output smaller than input: got %d want < %d", buf.Len(), len(input))
	}
}

func TestCompressWriterFallsBackToRaw(t *testing.T) {
	// 不可压缩随机数据：连续两块压不下去后应永久回退原始帧
	input := make([]byte, 3*compressBlockSize)
	rnd := rand.New(rand.NewSource(42))
	if _, err := rnd.Read(input); err != nil {
		t.Fatalf("rand: %v", err)
	}

	var buf bytes.Buffer
	w := newCompressWriter(&buf)
	if _, err := w.Write(input); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, compressedFrames, rawFrames := decodeFrames(t, buf.Bytes())
	if !bytes.Equal(out, input) {
		t.Fatalf("round trip mismatch: got %d bytes want %d", len(out), len(input))
	}
	if compressedFrames != 0 {
		t.Fatalf("expected all frames raw after fallback, got %d compressed", compressedFrames)
	}
	if rawFrames != 3 {
		t.Fatalf("expected 3 raw frames, got %d", rawFrames)
	}
	if !w.rawOnly {
		t.Fatal("expected writer to stay in raw-only mode")
	}
}

func TestCompressWriterSmallPayloadRaw(t *testing.T) {
	var buf bytes.Buffer
	w := newCompressWriter(&buf)
	payload := []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, compressedFrames, rawFrames := decodeFrames(t, buf.Bytes())
	if !bytes.Equal(out, payload) {
		t.Fatalf("round trip mismatch: %q", out)
	}
	if compressedFrames != 0 || rawFrames != 1 {
		t.Fatalf("expected 1 raw frame, got compressed=%d raw=%d", compressedFrames, rawFrames)
	}
}
