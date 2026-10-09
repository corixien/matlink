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
	"sync/atomic"
	"syscall"
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

	fmt.Fprintf(c, "{\"w\":%d,\"h\":%d,\"fps\":%d}\n", sess.W, sess.H, sess.FPS)
	s.OnState(fmt.Sprintf("Streaming %dx%d@%d to %s", sess.W, sess.H, sess.FPS, hello.Model))

	// Detect a dead peer: the app never sends after hello, so any read return means close.
	gone := make(chan struct{})
	go func() { io.Copy(io.Discard, c); close(gone) }()

	// Prefer the all-GPU gst pipeline; fall back to gst+ffmpeg if it cannot start or delivers nothing.
	modes := []bool{false}
	if gstVAOK() {
		modes = []bool{true, false}
	}
	for _, useVA := range modes {
		p, err := startPipeline(cap, sess, useVA)
		if err != nil {
			log.Printf("pipeline %s: %v", p.name, err)
			continue
		}
		fw := &frameWriter{w: c, start: time.Now()}
		quit := make(chan struct{})
		go func() {
			select {
			case <-done:
				p.kill()
			case <-gone:
				p.kill()
			case <-time.After(6 * time.Second):
				if fw.frames.Load() == 0 {
					log.Printf("pipeline %s: no frame after 6s", p.name)
					p.kill()
				}
			case <-quit:
			}
		}()
		log.Printf("encoder: %s", p.name)
		err = p.relay(p.out, fw)
		close(quit)
		p.kill()
		p.wait()
		log.Printf("stream ended (%s, %d frames): %v", p.name, fw.frames.Load(), err)
		if fw.frames.Load() == 0 && useVA {
			select {
			case <-done:
			case <-gone:
			default:
				gstVAFailed.Store(true)
				continue
			}
		}
		break
	}
	s.OnState("")
}

type logWriter struct{ prefix string }

func (l *logWriter) Write(p []byte) (int, error) {
	log.Print(l.prefix + strings.TrimSpace(string(p)))
	return len(p), nil
}

// pipeline is one capture-to-H.264 process chain whose stdout is relayed to the tablet.
type pipeline struct {
	name  string
	cmds  []*exec.Cmd
	out   io.Reader
	relay func(r io.Reader, fw *frameWriter) error
}

func (p *pipeline) kill() {
	for _, c := range p.cmds {
		if c.Process != nil {
			c.Process.Kill()
		}
	}
}

func (p *pipeline) wait() {
	for _, c := range p.cmds {
		c.Wait()
	}
}

// frameWriter frames access units as [u32 len][u64 pts_us][data], one TCP write each.
type frameWriter struct {
	w      io.Writer
	start  time.Time
	buf    []byte
	frames atomic.Int64
	fix    pocFixer
}

func (f *frameWriter) send(au []byte) error {
	au = f.fix.rewrite(au)
	f.buf = append(f.buf[:0], 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(f.buf[0:], uint32(len(au)))
	binary.BigEndian.PutUint64(f.buf[4:], uint64(time.Since(f.start).Microseconds()))
	f.buf = append(f.buf, au...)
	_, err := f.w.Write(f.buf)
	f.frames.Add(1)
	return err
}

func startPipeline(cap *Capture, sess Session, gstVA bool) (*pipeline, error) {
	if gstVA {
		return startGstVA(cap, sess)
	}
	return startFFmpeg(cap, sess)
}

// startGstVA runs the whole path on the GPU: PipeWire (DMA-BUF) -> VA postproc -> VA H.264 encoder -> GDP-framed access units.
// No raw frames cross a pipe and no second process is involved.
func startGstVA(cap *Capture, sess Session) (*pipeline, error) {
	p := &pipeline{name: "gst-va", relay: relayGDP}
	// PipeWire delivers variable-rate frames (framerate=0/1) and vah264enc then assumes 30 fps for rate control,
	// which would make the stream run at 30/fps times the requested bitrate. Scale the target to compensate.
	kbps := max(sess.Bitrate/1000*30/sess.FPS, 300)
	args := []string{"-q",
		"pipewiresrc", "fd=3", "path=" + strconv.Itoa(int(cap.NodeID)), "do-timestamp=true", "keepalive-time=250",
		"!", "vapostproc",
		"!", fmt.Sprintf("video/x-raw(memory:VAMemory),format=NV12,width=%d,height=%d", sess.W, sess.H),
		"!", "vah264enc", "rate-control=cbr", "bitrate=" + strconv.Itoa(kbps), "cpb-size=" + strconv.Itoa(max(kbps/15, 64)),
		"target-usage=7", "b-frames=0", "ref-frames=1", "key-int-max=1024", "aud=true", "cabac=false",
		"!", "video/x-h264,profile=constrained-baseline",
		"!", "h264parse", "config-interval=-1",
		"!", "video/x-h264,stream-format=byte-stream,alignment=au",
		"!", "gdppay",
		"!", "fdsink", "fd=1", "sync=false",
	}
	cmd := exec.Command("gst-launch-1.0", args...)
	cmd.ExtraFiles = []*os.File{cap.PWFile}
	cmd.Stderr = &logWriter{prefix: "gst: "}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return p, err
	}
	if err := cmd.Start(); err != nil {
		return p, err
	}
	p.cmds, p.out = []*exec.Cmd{cmd}, out
	return p, nil
}

// startFFmpeg is the fallback: gst converts to raw frames, threaded ffmpeg encodes (VAAPI or OpenH264).
func startFFmpeg(cap *Capture, sess Session) (*pipeline, error) {
	hw := vaapiOK()
	p := &pipeline{name: "gst+ffmpeg-sw", relay: relayAccessUnits}
	if hw {
		p.name = "gst+ffmpeg-vaapi"
	}
	pixFmt, ffPix := "I420", "yuv420p"
	if hw {
		pixFmt, ffPix = "NV12", "nv12"
	}
	gstArgs := []string{"-q",
		"pipewiresrc", "fd=3", "path=" + strconv.Itoa(int(cap.NodeID)), "do-timestamp=true", "keepalive-time=250",
		"!", "videoconvert", "n-threads=2", "!", "videoscale",
		"!", fmt.Sprintf("video/x-raw,format=%s,width=%d,height=%d", pixFmt, sess.W, sess.H),
		"!", "fdsink", "fd=1", "sync=false",
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return p, err
	}
	setPipeSize(pw, 1<<20)
	cmd := exec.Command("gst-launch-1.0", gstArgs...)
	cmd.ExtraFiles = []*os.File{cap.PWFile}
	cmd.Stdout = pw
	cmd.Stderr = &logWriter{prefix: "gst: "}
	args := []string{"-hide_banner", "-loglevel", "error", "-fflags", "nobuffer", "-probesize", "32", "-analyzeduration", "0"}
	if hw {
		args = append(args, "-vaapi_device", vaapiDevice)
	}
	args = append(args, "-f", "rawvideo", "-pix_fmt", ffPix, "-video_size", fmt.Sprintf("%dx%d", sess.W, sess.H),
		"-framerate", strconv.Itoa(sess.FPS), "-i", "pipe:0")
	brate := strconv.Itoa(sess.Bitrate)
	// keyframes only at the start: the TCP stream is lossless, and periodic IDRs cause latency spikes
	gop := "1000000"
	if hw {
		args = append(args, "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-b:v", brate, "-maxrate", brate,
			"-g", gop, "-bf", "0", "-async_depth", "1", "-compression_level", "7", "-profile:v", "constrained_baseline")
	} else {
		args = append(args, "-c:v", "libopenh264", "-b:v", brate, "-g", gop, "-slices", "4", "-threads", "4",
			"-allow_skip_frames", "0", "-profile:v", "constrained_baseline", "-flags", "+low_delay")
	}
	args = append(args, "-bsf:v", "h264_metadata=aud=insert", "-flush_packets", "1", "-f", "h264", "pipe:1")
	enc := exec.Command("ffmpeg", args...)
	enc.Stdin = pr
	enc.Stderr = &logWriter{prefix: "ffmpeg: "}
	out, err := enc.StdoutPipe()
	if err != nil {
		return p, err
	}
	if err := cmd.Start(); err != nil {
		notify("Matlink", "gst-launch-1.0 failed to start: "+err.Error())
		return p, err
	}
	pw.Close()
	if err := enc.Start(); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		notify("Matlink", "ffmpeg failed to start: "+err.Error())
		return p, err
	}
	pr.Close()
	p.cmds, p.out = []*exec.Cmd{cmd, enc}, out
	return p, nil
}

// setPipeSize enlarges a pipe buffer (default 64 KiB) so raw frames cross in few syscalls.
func setPipeSize(f *os.File, n int) {
	const fSetPipeSz = 1031
	syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), fSetPipeSz, uintptr(n))
}

const gdpHeaderLen = 62

// relayGDP reads GStreamer Data Protocol packets and forwards each buffer (exactly one access unit) at once.
func relayGDP(r io.Reader, fw *frameWriter) error {
	br := bufio.NewReaderSize(r, 1<<20)
	hdr := make([]byte, gdpHeaderLen)
	var buf []byte
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			return err
		}
		typ := binary.BigEndian.Uint16(hdr[4:6])
		n := int(binary.BigEndian.Uint32(hdr[6:10]))
		if hdr[0] != 1 || n > 16<<20 {
			return fmt.Errorf("gdp: bad packet (version %d, size %d)", hdr[0], n)
		}
		if cap(buf) < n {
			buf = make([]byte, n)
		}
		buf = buf[:n]
		if _, err := io.ReadFull(br, buf); err != nil {
			return err
		}
		if typ != 1 { // 1 = buffer; caps and events are skipped
			continue
		}
		if err := fw.send(buf); err != nil {
			return err
		}
	}
}

var startCode = []byte{0, 0, 1}

// relayAccessUnits splits an Annex-B stream at access unit delimiters (NAL type 9). The tail
// access unit is flushed as soon as the encoder stops writing, instead of waiting for the next
// frame's delimiter, which would add a full frame interval (or an unbounded stall on static content).
func relayAccessUnits(r io.Reader, fw *frameWriter) error {
	type chunk struct {
		b   []byte
		err error
	}
	chunks := make(chan chunk, 16)
	go func() {
		for {
			b := make([]byte, 64<<10)
			n, err := r.Read(b)
			if n > 0 {
				chunks <- chunk{b: b[:n]}
			}
			if err != nil {
				chunks <- chunk{err: err}
				return
			}
		}
	}()
	buf := make([]byte, 0, 1<<20)
	scanFrom := 5
	idle := time.NewTimer(time.Hour)
	defer idle.Stop()
	for {
		select {
		case c := <-chunks:
			if c.err != nil {
				return c.err
			}
			buf = append(buf, c.b...)
			for {
				i := findAUD(buf, scanFrom)
				if i < 0 {
					scanFrom = max(5, len(buf)-4)
					break
				}
				if err := fw.send(buf[:i]); err != nil {
					return err
				}
				buf = append(buf[:0], buf[i:]...)
				scanFrom = 5
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(2 * time.Millisecond)
		case <-idle.C:
			if len(buf) > 0 {
				if err := fw.send(buf); err != nil {
					return err
				}
				buf = buf[:0]
				scanFrom = 5
			}
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
	gstVAOnce sync.Once
	gstVAYes  bool

	gstVAFailed atomic.Bool
)

// gstVAOK reports whether the all-GPU gst pipeline (VA postproc + VA H.264 encoder + GDP) works here.
func gstVAOK() bool {
	gstVAOnce.Do(func() {
		if _, err := os.Stat(vaapiDevice); err != nil {
			return
		}
		for _, el := range []string{"vapostproc", "vah264enc", "h264parse", "gdppay"} {
			if exec.Command("gst-inspect-1.0", "--exists", el).Run() != nil {
				return
			}
		}
		err := exec.Command("gst-launch-1.0", "-q", "videotestsrc", "num-buffers=3", "!", "video/x-raw,format=BGRx,width=640,height=480,framerate=30/1",
			"!", "vapostproc", "!", "video/x-raw(memory:VAMemory),format=NV12", "!", "vah264enc", "!", "fakesink").Run()
		gstVAYes = err == nil
	})
	return gstVAYes && !gstVAFailed.Load()
}

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
