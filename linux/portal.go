package main

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalDest  = "org.freedesktop.portal.Desktop"
	portalPath  = "/org/freedesktop/portal/desktop"
	screencastI = "org.freedesktop.portal.ScreenCast"
	requestI    = "org.freedesktop.portal.Request"
)

var tokenCounter atomic.Uint64

// Capture is a live virtual-monitor ScreenCast session.
type Capture struct {
	conn    *dbus.Conn
	session dbus.ObjectPath
	NodeID  uint32
	Size    [2]int32
	Pos     [2]int32
	PWFile  *os.File
}

func (c *Capture) Close() {
	if c.PWFile != nil {
		c.PWFile.Close()
	}
	if c.conn != nil {
		if c.session != "" {
			c.conn.Object(portalDest, c.session).Call("org.freedesktop.portal.Session.Close", 0)
		}
		c.conn.Close()
	}
}

func newToken() string {
	return fmt.Sprintf("matlink_%d_%d", os.Getpid(), tokenCounter.Add(1))
}

// request performs a portal call that answers through a Request/Response signal.
func request(conn *dbus.Conn, ch chan *dbus.Signal, timeout time.Duration, method string, args ...any) (map[string]dbus.Variant, error) {
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	// options map is always the last argument and carries handle_token
	opts := args[len(args)-1].(map[string]dbus.Variant)
	tok := newToken()
	opts["handle_token"] = dbus.MakeVariant(tok)
	reqPath := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/portal/desktop/request/%s/%s", sender, tok))
	if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(reqPath), dbus.WithMatchInterface(requestI), dbus.WithMatchMember("Response")); err != nil {
		return nil, err
	}
	defer conn.RemoveMatchSignal(dbus.WithMatchObjectPath(reqPath), dbus.WithMatchInterface(requestI), dbus.WithMatchMember("Response"))
	var handle dbus.ObjectPath
	if err := conn.Object(portalDest, portalPath).Call(method, 0, args...).Store(&handle); err != nil {
		return nil, err
	}
	deadline := time.After(timeout)
	for {
		select {
		case s := <-ch:
			if s.Path != reqPath || s.Name != requestI+".Response" {
				continue
			}
			code := s.Body[0].(uint32)
			res := s.Body[1].(map[string]dbus.Variant)
			if code != 0 {
				return nil, fmt.Errorf("portal request denied or cancelled (code %d)", code)
			}
			return res, nil
		case <-deadline:
			return nil, fmt.Errorf("portal request timed out")
		}
	}
}

// OpenCapture creates a virtual monitor of the requested size and returns its PipeWire stream.
func OpenCapture(w, h int, restoreToken string) (*Capture, string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, "", err
	}
	c := &Capture{conn: conn}
	ch := make(chan *dbus.Signal, 32)
	conn.Signal(ch)
	fail := func(err error) (*Capture, string, error) { c.Close(); return nil, "", err }

	res, err := request(conn, ch, 10*time.Second, screencastI+".CreateSession", map[string]dbus.Variant{
		"session_handle_token": dbus.MakeVariant(newToken()),
	})
	if err != nil {
		return fail(err)
	}
	c.session = dbus.ObjectPath(res["session_handle"].Value().(string))

	opts := map[string]dbus.Variant{
		"types":        dbus.MakeVariant(uint32(4)), // VIRTUAL
		"cursor_mode":  dbus.MakeVariant(uint32(2)), // embedded cursor
		"persist_mode": dbus.MakeVariant(uint32(2)),
		"multiple":     dbus.MakeVariant(false),
	}
	if restoreToken != "" {
		opts["restore_token"] = dbus.MakeVariant(restoreToken)
	}
	if _, err = request(conn, ch, 30*time.Second, screencastI+".SelectSources", c.session, opts); err != nil {
		return fail(err)
	}
	res, err = request(conn, ch, 120*time.Second, screencastI+".Start", c.session, "", map[string]dbus.Variant{})
	if err != nil {
		return fail(err)
	}
	newTok := ""
	if v, ok := res["restore_token"]; ok {
		newTok, _ = v.Value().(string)
	}
	streams, _ := res["streams"].Value().([][]any)
	if len(streams) == 0 {
		return fail(fmt.Errorf("portal returned no streams"))
	}
	c.NodeID = streams[0][0].(uint32)
	if props, ok := streams[0][1].(map[string]dbus.Variant); ok {
		if v, ok := props["size"]; ok {
			if s, ok := v.Value().([]int32); ok && len(s) == 2 {
				c.Size = [2]int32{s[0], s[1]}
			}
		}
		if v, ok := props["position"]; ok {
			if s, ok := v.Value().([]int32); ok && len(s) == 2 {
				c.Pos = [2]int32{s[0], s[1]}
			}
		}
	}
	var fd dbus.UnixFD
	if err = conn.Object(portalDest, portalPath).Call(screencastI+".OpenPipeWireRemote", 0, c.session, map[string]dbus.Variant{}).Store(&fd); err != nil {
		return fail(err)
	}
	c.PWFile = os.NewFile(uintptr(fd), "pipewire")
	return c, newTok, nil
}
