package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"time"
)

type sink struct{ frames [][]byte }

func (s *sink) Write(p []byte) (int, error) {
	n := int(binary.BigEndian.Uint32(p[0:4]))
	s.frames = append(s.frames, append([]byte(nil), p[12:12+n]...))
	return len(p), nil
}

func TestRelayGDP(t *testing.T) {
	d, err := os.ReadFile(os.Getenv("GDP_SAMPLE"))
	if err != nil {
		t.Skip("no GDP_SAMPLE")
	}
	s := &sink{}
	fw := &frameWriter{w: s, start: time.Now()}
	if err := relayGDP(bytes.NewReader(d), fw); err == nil {
		t.Fatal("expected EOF")
	}
	if len(s.frames) != 20 {
		t.Fatalf("frames = %d, want 20", len(s.frames))
	}
	if !bytes.HasPrefix(s.frames[1], []byte{0, 0, 0, 1, 9}) {
		t.Fatalf("frame does not start with AUD: %x", s.frames[1][:8])
	}
}

func TestPocFixerSample(t *testing.T) {
	d, err := os.ReadFile(os.Getenv("GDP_SAMPLE"))
	if err != nil {
		t.Skip("no GDP_SAMPLE")
	}
	s := &sink{}
	relayGDP(bytes.NewReader(d), &frameWriter{w: s, start: time.Now()})
	if err := os.WriteFile(os.Getenv("OUT_H264"), bytes.Join(s.frames, nil), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyBitsAndEscape(t *testing.T) {
	src := []byte{0xA5, 0x00, 0x00, 0x01, 0xFF, 0x3C, 0x81}
	for from := 0; from < 20; from++ {
		for to := from; to <= len(src)*8; to += 3 {
			for pre := 0; pre < 9; pre++ {
				w := &bitWriter{}
				for i := 0; i < pre; i++ {
					w.bit(1)
				}
				w.copyBits(src, from, to)
				r := &bitReader{b: w.b, pos: pre}
				for i := from; i < to; i++ {
					want := int(src[i>>3] >> (7 - i&7) & 1)
					if got := r.u(1); got != want {
						t.Fatalf("pre=%d from=%d to=%d bit %d: got %d", pre, from, to, i, got)
					}
				}
			}
		}
	}
	raw := []byte{0, 0, 1, 0, 0, 0, 0, 0, 3, 0, 0}
	if got := unescape(escape(raw)); !bytes.Equal(got, raw) {
		t.Fatalf("escape roundtrip: %x", got)
	}
	w := &bitWriter{}
	for _, v := range []int{0, 1, 2, 7, 100} {
		w.ue(v)
	}
	r := &bitReader{b: w.b}
	for _, v := range []int{0, 1, 2, 7, 100} {
		if got := r.ue(); got != v {
			t.Fatalf("ue: got %d want %d", got, v)
		}
	}
}

func TestRelayAccessUnitsIdleFlush(t *testing.T) {
	au := func(n byte) []byte { return []byte{0, 0, 0, 1, 9, 0x10, 0, 0, 1, 0x21, n, n, n} }
	pr, pw := pipePair()
	s := &sink{}
	done := make(chan error, 1)
	go func() { done <- relayAccessUnits(pr, &frameWriter{w: s, start: time.Now()}) }()
	pw.Write(au(1))
	time.Sleep(20 * time.Millisecond)
	if len(s.frames) != 1 {
		t.Fatalf("tail AU not flushed on idle: %d frames", len(s.frames))
	}
	pw.Write(append(au(2), au(3)...))
	time.Sleep(20 * time.Millisecond)
	pw.Close()
	<-done
	if len(s.frames) != 3 || !bytes.Equal(s.frames[2], au(3)) {
		t.Fatalf("frames = %d", len(s.frames))
	}
}

func pipePair() (*os.File, *os.File) {
	r, w, _ := os.Pipe()
	return r, w
}
