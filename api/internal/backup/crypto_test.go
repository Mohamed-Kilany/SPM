package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func TestSealAndOpenStreamAcrossChunks(t *testing.T) {
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 2*chunkSize + 17} {
		t.Run(fmt.Sprintf("%d-bytes", size), func(t *testing.T) {
			plain := make([]byte, size)
			if _, err := rand.Read(plain); err != nil {
				t.Fatalf("generate plaintext: %v", err)
			}
			key := testKey(t)
			var sealed bytes.Buffer
			if err := SealStream(&sealed, bytes.NewReader(plain), key); err != nil {
				t.Fatalf("SealStream(%d): %v", size, err)
			}
			if !bytes.HasPrefix(sealed.Bytes(), []byte(encryptedMagic)) {
				t.Fatal("encrypted output has no magic header")
			}
			var opened bytes.Buffer
			if err := OpenStream(&opened, bytes.NewReader(sealed.Bytes()), key); err != nil {
				t.Fatalf("OpenStream(%d): %v", size, err)
			}
			if !bytes.Equal(opened.Bytes(), plain) {
				t.Fatalf("round trip mismatch for %d bytes", size)
			}
		})
	}
}

func TestOpenStreamRejectsWrongKeyAndCorruption(t *testing.T) {
	key := testKey(t)
	plain := bytes.Repeat([]byte("backup payload "), 1000)
	var sealed bytes.Buffer
	if err := SealStream(&sealed, bytes.NewReader(plain), key); err != nil {
		t.Fatalf("SealStream: %v", err)
	}

	wrong := testKey(t)
	var output bytes.Buffer
	if err := OpenStream(&output, bytes.NewReader(sealed.Bytes()), wrong); err == nil || !strings.Contains(err.Error(), "key id") {
		t.Fatalf("OpenStream wrong key error = %v", err)
	}

	tampered := append([]byte(nil), sealed.Bytes()...)
	tampered[len(tampered)-8] ^= 0xff
	output.Reset()
	if err := OpenStream(&output, bytes.NewReader(tampered), key); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("OpenStream tampered error = %v", err)
	}

	truncated := sealed.Bytes()[:len(sealed.Bytes())-4]
	output.Reset()
	if err := OpenStream(&output, bytes.NewReader(truncated), key); err == nil {
		t.Fatal("OpenStream accepted a truncated stream")
	}

	var empty bytes.Buffer
	if err := OpenStream(&empty, bytes.NewReader(nil), key); err == nil {
		t.Fatal("OpenStream accepted an empty stream")
	}
}

func TestSealStreamRejectsShortKey(t *testing.T) {
	var output bytes.Buffer
	if err := SealStream(&output, strings.NewReader("data"), make([]byte, KeySize-1)); err == nil {
		t.Fatal("SealStream accepted a short key")
	}
}

func TestOpenStreamDetectsReorderedChunks(t *testing.T) {
	key := testKey(t)
	plain := make([]byte, 2*chunkSize)
	if _, err := rand.Read(plain); err != nil {
		t.Fatalf("generate plaintext: %v", err)
	}
	var sealed bytes.Buffer
	if err := SealStream(&sealed, bytes.NewReader(plain), key); err != nil {
		t.Fatalf("SealStream: %v", err)
	}
	frames := splitFrames(t, sealed.Bytes())
	if len(frames) < 3 {
		t.Fatalf("expected at least two data chunks plus trailer, got %d", len(frames))
	}
	// Swap the first two data chunks; the index is authenticated, so decryption
	// must fail even though both chunks are individually valid.
	reordered := append([]byte(nil), sealed.Bytes()[:len(encryptedMagic)+keyIDSize]...)
	reordered = append(reordered, frames[1]...)
	reordered = append(reordered, frames[0]...)
	for _, frame := range frames[2:] {
		reordered = append(reordered, frame...)
	}
	var output bytes.Buffer
	if err := OpenStream(&output, bytes.NewReader(reordered), key); err == nil {
		t.Fatal("OpenStream accepted reordered chunks")
	}
}

func splitFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	offset := len(encryptedMagic) + keyIDSize
	var frames [][]byte
	for offset < len(data) {
		if offset+4 > len(data) {
			t.Fatalf("truncated frame header at %d", offset)
		}
		length := int(data[offset])<<24 | int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		end := offset + 4 + 12 + length + 16
		if end > len(data) {
			t.Fatalf("truncated frame at %d", offset)
		}
		frames = append(frames, data[offset:end])
		offset = end
	}
	return frames
}

func TestKeyIDIsStableAndShort(t *testing.T) {
	key := make([]byte, KeySize)
	first := KeyID(key)
	second := KeyID(key)
	if first != second || len(first) != keyIDSize*2 {
		t.Fatalf("KeyID = %q / %q", first, second)
	}
	other := make([]byte, KeySize)
	other[0] = 1
	if KeyID(other) == first {
		t.Fatal("different keys produced the same identifier")
	}
}

func TestOpenStreamReturnsReaderErrors(t *testing.T) {
	key := testKey(t)
	var sealed bytes.Buffer
	if err := SealStream(&sealed, bytes.NewReader([]byte("payload")), key); err != nil {
		t.Fatalf("SealStream: %v", err)
	}
	reader := &failingReader{data: sealed.Bytes(), failAfter: len(encryptedMagic) + keyIDSize + 4}
	var output bytes.Buffer
	err := OpenStream(&output, reader, key)
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) && !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("OpenStream error = %v, want truncation", err)
	}
}

type failingReader struct {
	data      []byte
	offset    int
	failAfter int
}

func (f *failingReader) Read(target []byte) (int, error) {
	if f.offset >= len(f.data) {
		return 0, io.EOF
	}
	if f.offset >= f.failAfter {
		return 0, io.ErrUnexpectedEOF
	}
	limit := f.failAfter - f.offset
	if limit > len(target) {
		limit = len(target)
	}
	read := copy(target, f.data[f.offset:f.offset+limit])
	f.offset += read
	return read, nil
}
