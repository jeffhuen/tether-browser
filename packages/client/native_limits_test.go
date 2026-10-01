package client

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestNativeMessageChromeLimits(t *testing.T) {
	// Legal extension->host screenshots above the old 16MiB ceiling must not
	// disconnect the host or consume the following frame.
	payload := bytes.Repeat([]byte("x"), 16*1024*1024+1)
	var input bytes.Buffer
	_ = binary.Write(&input, binary.LittleEndian, uint32(len(payload)))
	input.Write(payload)
	next := []byte(`{"type":"heartbeat"}`)
	_ = binary.Write(&input, binary.LittleEndian, uint32(len(next)))
	input.Write(next)
	got, err := ReadNativeMessage(&input)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("legal large Chrome frame rejected: %v", err)
	}
	got, err = ReadNativeMessage(&input)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatalf("following frame lost: %v", err)
	}
	input.Reset()
	_ = binary.Write(&input, binary.LittleEndian, uint32(0xffffffff))
	input.WriteString("not to be drained")
	if _, err := ReadNativeMessage(&input); err == nil {
		t.Fatal("malformed 4GiB frame accepted")
	}
	if input.String() != "not to be drained" {
		t.Fatal("malformed frame was drained")
	}
	var output bytes.Buffer
	if err := WriteNativeMessage(&output, make([]byte, (1<<20)+1)); err == nil || output.Len() != 0 {
		t.Fatal("oversized outgoing frame was partially sent")
	}
}

type nativeRepeatedByte struct{}

func (nativeRepeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestNativeAcceptsExactChrome64MiBBoundary(t *testing.T) {
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], 64<<20)
	wire := io.MultiReader(bytes.NewReader(header[:]), strings.NewReader(`"`), io.LimitReader(nativeRepeatedByte{}, (64<<20)-2), strings.NewReader(`"`))
	payload, err := ReadNativeMessage(wire)
	if err != nil || len(payload) != 64<<20 || payload[0] != '"' || payload[len(payload)-1] != '"' {
		t.Fatalf("legal Chrome maximum frame rejected: %v", err)
	}
	binary.LittleEndian.PutUint32(header[:], (64<<20)+1)
	if _, err := ReadNativeMessage(bytes.NewReader(header[:])); err == nil {
		t.Fatal("frame beyond Chrome maximum accepted")
	}
}
