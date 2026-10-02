package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hello is the first line sent by the Android app.
type Hello struct {
	Version int    `json:"v"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	DPI     int    `json:"dpi"`
	Model   string `json:"model"`
	MaxH    int    `json:"maxH"` // 0 = no cap
	FPS     int    `json:"fps"`
	Quality string `json:"quality"`
}

// Session describes the effective parameters of one stream.
type Session struct {
	W, H, FPS, Bitrate int
	Scale              float64
}

var qualityBPP = map[string]float64{"low": 0.05, "medium": 0.09, "high": 0.14, "ultra": 0.22}

func even(v int) int { return v &^ 1 }

// resolve merges tablet request and PC settings into the effective session.
func resolve(h Hello, c Config) Session {
	w, ht := h.W, h.H
	if w < 320 || ht < 320 {
		w, ht = 1920, 1200
	}
	if h.MaxH > 0 && min(w, ht) > h.MaxH {
		f := float64(h.MaxH) / float64(min(w, ht))
		w, ht = int(float64(w)*f), int(float64(ht)*f)
	}
	if c.Resolution != "auto" {
		var fw, fh int
		if _, err := fmt.Sscanf(c.Resolution, "%dx%d", &fw, &fh); err == nil {
			// keep orientation of the tablet
			if (w < ht) != (fw < fh) {
				fw, fh = fh, fw
			}
			w, ht = fw, fh
		}
	}
	s := Session{W: even(w), H: even(ht), FPS: 60}
	if h.FPS > 0 {
		s.FPS = h.FPS
	}
	if c.FPS > 0 {
		s.FPS = c.FPS
	}
	q := h.Quality
	if c.Quality != "auto" {
		q = c.Quality
	}
	bpp, ok := qualityBPP[q]
	if !ok {
		bpp = qualityBPP["high"]
	}
	s.Bitrate = int(float64(s.W*s.H*s.FPS) * bpp)
	s.Scale = 1
	if c.Scale == "auto" {
		switch {
		case h.DPI >= 280:
			s.Scale = 1.5
		case h.DPI >= 200:
			s.Scale = 1.25
		}
	} else if f, err := strconv.ParseFloat(c.Scale, 64); err == nil && f >= 1 {
		s.Scale = f
	}
	return s
}

// Server accepts tablet connections.
type Server struct {
	sessMu  sync.Mutex // one capture session at a time
	ln      net.Listener
	mu      sync.Mutex
	cancel  func()
	OnState func(state string)
}

func (s *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	s.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			if s.cancel != nil {
				s.cancel() // newest connection wins
			}
			done := make(chan struct{})
			var once sync.Once
			s.cancel = func() { once.Do(func() { close(done); c.Close() }) }
			s.mu.Unlock()
			go func() {
				s.handle(c, done)
				c.Close()
			}()
		}
	}()
	return nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

func (s *Server) handle(c net.Conn, done chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in session: %v\n%s", r, debug.Stack())
			s.OnState("")
		}
	}()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return
	}
	c.SetReadDeadline(time.Time{})
	var hello Hello
	if json.Unmarshal(line, &hello) != nil {
		return
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	select {
	case <-done: // superseded while waiting
		return
	default:
	}
	conf := getConfig()
	if !conf.Enabled {
		return
	}
	sess := resolve(hello, conf)
	log.Printf("tablet %q wants %dx%d dpi %d -> session %+v", hello.Model, hello.W, hello.H, hello.DPI, sess)
	s.OnState("Starting " + fmt.Sprintf("%dx%d", sess.W, sess.H) + "...")

	cap, tok, err := OpenCapture(sess.W, sess.H, conf.RestoreToken)
	if err != nil {
		log.Printf("capture: %v", err)
		notify("Matlink", "Could not create virtual display: "+err.Error())
		s.OnState("Error: " + err.Error())
		return
	}
	defer cap.Close()
	if tok != "" && tok != conf.RestoreToken {
		updateConfig(func(c *Config) { c.RestoreToken = tok })
	}
	if _, err := ConfigureVirtualOutput(sess.W, sess.H, sess.FPS, sess.Scale, conf.Position); err != nil {
		log.Printf("display config: %v", err)
	}

	hw := vaapiOK()
	pixFmt, ffPix := "I420", "yuv420p"
	if hw {
		pixFmt, ffPix = "NV12", "nv12"
	}
	pipeline := []string{"-q",
		"pipewiresrc", "fd=3", "path=" + strconv.Itoa(int(cap.NodeID)), "do-timestamp=true", "keepalive-time=250",
		"!", "videoconvert", "n-threads=2", "!", "videoscale",
		"!", fmt.Sprintf("video/x-raw,format=%s,width=%d,height=%d", pixFmt, sess.W, sess.H),
		"!", "fdsink", "fd=1", "sync=false",
	}
	// gst only captures and converts; ffmpeg's threaded OpenH264 encodes (gst's encoder is single-threaded and too slow).
	pr, pw, err := os.Pipe()
	if err != nil {
		return
	}
	cmd := exec.Command("gst-launch-1.0", pipeline...)
	cmd.ExtraFiles = []*os.File{cap.PWFile}
	cmd.Stdout = pw
	cmd.Stderr = &logWriter{prefix: "gst: "}
	args := []string{"-hide_banner", "-loglevel", "error", "-fflags", "nobuffer", "-probesize", "32"}
	if hw {
		args = append(args, "-vaapi_device", vaapiDevice)
	}
	args = append(args, "-f", "rawvideo", "-pix_fmt", ffPix, "-video_size", fmt.Sprintf("%dx%d", sess.W, sess.H),
		"-framerate", strconv.Itoa(sess.FPS), "-i", "pipe:0")
	brate := strconv.Itoa(sess.Bitrate)
	if hw {
		args = append(args, "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-b:v", brate, "-maxrate", brate,
			"-g", strconv.Itoa(sess.FPS*2), "-bf", "0", "-async_depth", "1", "-profile:v", "constrained_baseline")
	} else {
		args = append(args, "-c:v", "libopenh264", "-b:v", brate, "-g", strconv.Itoa(sess.FPS*2), "-slices", "4", "-threads", "4",
			"-allow_skip_frames", "0", "-profile:v", "constrained_baseline", "-flags", "+low_delay")
	}
	args = append(args, "-bsf:v", "h264_metadata=aud=insert", "-f", "h264", "pipe:1")
	log.Printf("encoder: hardware(vaapi)=%v", hw)
	enc := exec.Command("ffmpeg", args...)
	enc.Stdin = pr
	enc.Stderr = &logWriter{prefix: "ffmpeg: "}
	out, err := enc.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		notify("Matlink", "gst-launch-1.0 failed to start: "+err.Error())
		return
	}
	pw.Close()
	if err := enc.Start(); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		notify("Matlink", "ffmpeg failed to start: "+err.Error())
		return
	}
	pr.Close()
	kill := func() { cmd.Process.Kill(); enc.Process.Kill() }
	defer func() { kill(); cmd.Wait(); enc.Wait() }()
	go func() { <-done; kill() }()

	fmt.Fprintf(c, "{\"w\":%d,\"h\":%d,\"fps\":%d}\n", sess.W, sess.H, sess.FPS)
	s.OnState(fmt.Sprintf("Streaming %dx%d@%d to %s", sess.W, sess.H, sess.FPS, hello.Model))

	// Detect a dead peer: the app never sends after hello, so any read return means close.
	go func() { io.Copy(io.Discard, c); kill() }()

	br := bufio.NewReaderSize(out, 1<<20)
	err = relayAccessUnits(br, c)
	log.Printf("stream ended: %v", err)
	s.OnState("")
}

type logWriter struct{ prefix string }

func (l *logWriter) Write(p []byte) (int, error) {
	log.Print(l.prefix + strings.TrimSpace(string(p)))
	return len(p), nil
}

var startCode = []byte{0, 0, 1}

// relayAccessUnits splits an Annex-B stream at access unit delimiters (NAL type 9)
// and writes [u32 len][u64 pts_us][data] frames.
func relayAccessUnits(r io.Reader, w io.Writer) error {
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 256<<10)
	start := time.Now()
	scanFrom := 5
	send := func(au []byte) error {
		hdr := make([]byte, 12)
		binary.BigEndian.PutUint32(hdr[0:], uint32(len(au)))
		binary.BigEndian.PutUint64(hdr[4:], uint64(time.Since(start).Microseconds()))
		_, err := w.Write(append(hdr, au...))
		return err
	}
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		for {
			i := findAUD(buf, scanFrom)
			if i < 0 {
				scanFrom = max(5, len(buf)-4)
				break
			}
			if werr := send(buf[:i]); werr != nil {
				return werr
			}
			buf = append(buf[:0], buf[i:]...)
			scanFrom = 5
		}
		if err != nil {
			return err
		}
	}
}

// findAUD returns the index of the start code (00 00 01 or 00 00 00 01) preceding an AUD NAL at or after from.
func findAUD(b []byte, from int) int {
	for {
		if from >= len(b) {
			return -1
		}
		i := bytes.Index(b[from:], startCode)
		if i < 0 || from+i+3 >= len(b) {
			return -1
		}
		i += from
		if b[i+3]&31 == 9 {
			if i > 0 && b[i-1] == 0 {
				return i - 1
			}
			return i
		}
		from = i + 3
	}
}

const vaapiDevice = "/dev/dri/renderD128"

var (
	vaapiOnce sync.Once
	vaapiYes  bool
)

// vaapiOK reports whether ffmpeg can hardware-encode H.264 through VAAPI (needs the freeworld media driver on Fedora).
func vaapiOK() bool {
	vaapiOnce.Do(func() {
		if _, err := os.Stat(vaapiDevice); err != nil {
			return
		}
		err := exec.Command("ffmpeg", "-v", "error", "-vaapi_device", vaapiDevice, "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
			"-frames:v", "2", "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-f", "null", "-").Run()
		vaapiYes = err == nil
	})
	return vaapiYes
}
