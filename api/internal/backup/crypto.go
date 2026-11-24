package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	// KeySize is the AES-256 key length in bytes.
	KeySize = 32

	// encryptedMagic identifies a backup bundle encrypted by SealStream.
	encryptedMagic = "SPMBK1\n"

	keyIDSize = 8

	// chunkSize bounds the plaintext buffered for one AES-GCM chunk.
	chunkSize = 1 << 20

	// maxChunkSize rejects corrupt length prefixes before allocating.
	maxChunkSize = 16 << 20
)

// KeyID returns a short, non-secret identifier for a key. It is recorded in
// the encrypted header so an operator can tell which key a bundle needs
// without exposing the key itself.
func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:keyIDSize])
}

func keyIDBytes(key []byte) []byte {
	sum := sha256.Sum256(key)
	return sum[:keyIDSize]
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("backup: encryption key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: create GCM: %w", err)
	}
	return gcm, nil
}

func chunkAAD(index uint64, length uint32) []byte {
	aad := make([]byte, 12)
	binary.BigEndian.PutUint64(aad[:8], index)
	binary.BigEndian.PutUint32(aad[8:], length)
	return aad
}

// SealStream encrypts src into dst with AES-256-GCM. The output starts with a
// magic header and an 8-byte key identifier, followed by length-prefixed,
// individually authenticated chunks and an authenticated zero-length trailer.
// The chunk index and length are bound as additional authenticated data, so
// reordered, truncated or corrupted bundles fail closed.
func SealStream(dst io.Writer, src io.Reader, key []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	header := make([]byte, 0, len(encryptedMagic)+keyIDSize)
	header = append(header, encryptedMagic...)
	header = append(header, keyIDBytes(key)...)
	if _, err := dst.Write(header); err != nil {
		return fmt.Errorf("backup: write encrypted header: %w", err)
	}

	buffer := make([]byte, chunkSize)
	var index uint64
	for {
		read, readErr := io.ReadFull(src, buffer)
		if read > 0 {
			if err := writeChunk(dst, gcm, index, buffer[:read]); err != nil {
				return err
			}
			index++
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("backup: read plaintext: %w", readErr)
		}
	}
	return writeChunk(dst, gcm, index, nil)
}

func writeChunk(dst io.Writer, gcm cipher.AEAD, index uint64, plain []byte) error {
	if len(plain) > maxChunkSize {
		return fmt.Errorf("backup: chunk %d exceeds %d bytes", index, maxChunkSize)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("backup: generate nonce: %w", err)
	}
	length := uint32(len(plain))
	aad := chunkAAD(index, length)
	sealed := gcm.Seal(nil, nonce, plain, aad)

	frame := make([]byte, 4+len(nonce)+len(sealed))
	binary.BigEndian.PutUint32(frame[:4], length)
	copy(frame[4:], nonce)
	copy(frame[4+len(nonce):], sealed)
	if _, err := dst.Write(frame); err != nil {
		return fmt.Errorf("backup: write chunk %d: %w", index, err)
	}
	return nil
}

// OpenStream decrypts an encrypted bundle from src into dst.
func OpenStream(dst io.Writer, src io.Reader, key []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	header := make([]byte, len(encryptedMagic)+keyIDSize)
	if _, err := io.ReadFull(src, header); err != nil {
		return fmt.Errorf("backup: read encrypted header: %w", err)
	}
	if string(header[:len(encryptedMagic)]) != encryptedMagic {
		return errors.New("backup: not an SPM encrypted bundle")
	}
	expectedID := keyIDBytes(key)
	if !equalBytes(header[len(encryptedMagic):], expectedID) {
		return fmt.Errorf("backup: bundle key id %s does not match the supplied key %s",
			hex.EncodeToString(header[len(encryptedMagic):]), KeyID(key))
	}

	reader := &decryptReader{src: src, gcm: gcm}
	if _, err := io.Copy(dst, reader); err != nil {
		return err
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for i := range left {
		diff |= left[i] ^ right[i]
	}
	return diff == 0
}

type decryptReader struct {
	src   io.Reader
	gcm   cipher.AEAD
	index uint64
	buf   []byte
	done  bool
}

func (d *decryptReader) Read(target []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.nextChunk(); err != nil {
			return 0, err
		}
	}
	read := copy(target, d.buf)
	d.buf = d.buf[read:]
	return read, nil
}

func (d *decryptReader) nextChunk() error {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(d.src, lengthBytes[:]); err != nil {
		return fmt.Errorf("backup: truncated encrypted bundle at chunk %d: %w", d.index, err)
	}
	length := binary.BigEndian.Uint32(lengthBytes[:])
	if length > maxChunkSize {
		return fmt.Errorf("backup: encrypted chunk %d has invalid length %d", d.index, length)
	}
	nonce := make([]byte, d.gcm.NonceSize())
	if _, err := io.ReadFull(d.src, nonce); err != nil {
		return fmt.Errorf("backup: truncated encrypted bundle at chunk %d: %w", d.index, err)
	}
	sealed := make([]byte, int(length)+d.gcm.Overhead())
	if _, err := io.ReadFull(d.src, sealed); err != nil {
		return fmt.Errorf("backup: truncated encrypted bundle at chunk %d: %w", d.index, err)
	}
	plain, err := d.gcm.Open(nil, nonce, sealed, chunkAAD(d.index, length))
	if err != nil {
		return fmt.Errorf("backup: encrypted bundle authentication failed at chunk %d", d.index)
	}
	if length == 0 {
		var trailing [1]byte
		read, trailingErr := d.src.Read(trailing[:])
		if read > 0 {
			return errors.New("backup: trailing data after the encrypted bundle trailer")
		}
		if trailingErr != nil && !errors.Is(trailingErr, io.EOF) {
			return fmt.Errorf("backup: read encrypted bundle trailer: %w", trailingErr)
		}
		d.done = true
		return nil
	}
	d.buf = plain
	d.index++
	return nil
}
