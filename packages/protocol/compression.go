package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Frame encoding flags.
const (
	FormatRaw  byte = 0x00
	FormatZstd byte = 0x01

	// CompressionThreshold is the minimum payload size in bytes to trigger zstd compression.
	CompressionThreshold = 1024
)

var (
	encoderPool sync.Pool
	decoderPool sync.Pool
)

func init() {
	encoderPool = sync.Pool{
		New: func() any {
			enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
			if err != nil {
				panic(err)
			}
			return enc
		},
	}
	decoderPool = sync.Pool{
		New: func() any {
			dec, err := zstd.NewReader(nil)
			if err != nil {
				panic(err)
			}
			return dec
		},
	}
}

// CompressPayload wraps a byte slice with framing headers and optional zstd compression.
func CompressPayload(data []byte) ([]byte, error) {
	uncompressedLen := uint32(len(data))
	if len(data) < CompressionThreshold {
		// Small payload: send uncompressed with 5-byte header.
		buf := make([]byte, 5+len(data))
		buf[0] = FormatRaw
		binary.BigEndian.PutUint32(buf[1:5], uncompressedLen)
		copy(buf[5:], data)
		return buf, nil
	}

	enc := encoderPool.Get().(*zstd.Encoder)
	defer encoderPool.Put(enc)

	compressed := enc.EncodeAll(data, make([]byte, 0, len(data)/2))

	buf := make([]byte, 5+len(compressed))
	buf[0] = FormatZstd
	binary.BigEndian.PutUint32(buf[1:5], uncompressedLen)
	copy(buf[5:], compressed)
	return buf, nil
}

// DecompressPayload decodes a framed payload, decompressing with zstd if required.
func DecompressPayload(framed []byte) ([]byte, error) {
	if len(framed) < 5 {
		return nil, errors.New("framed payload too short (< 5 bytes)")
	}

	format := framed[0]
	uncompressedLen := binary.BigEndian.Uint32(framed[1:5])
	payload := framed[5:]

	switch format {
	case FormatRaw:
		if uint32(len(payload)) != uncompressedLen {
			return nil, fmt.Errorf("length mismatch: header %d, actual %d", uncompressedLen, len(payload))
		}
		out := make([]byte, len(payload))
		copy(out, payload)
		return out, nil

	case FormatZstd:
		dec := decoderPool.Get().(*zstd.Decoder)
		defer decoderPool.Put(dec)

		decompressed, err := dec.DecodeAll(payload, make([]byte, 0, uncompressedLen))
		if err != nil {
			return nil, fmt.Errorf("zstd decompression failed: %w", err)
		}
		if uint32(len(decompressed)) != uncompressedLen {
			return nil, fmt.Errorf("decompressed length mismatch: expected %d, got %d", uncompressedLen, len(decompressed))
		}
		return decompressed, nil

	default:
		return nil, fmt.Errorf("unknown frame format: 0x%02x", format)
	}
}
