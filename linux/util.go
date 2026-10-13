package main

import (
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

var (
	notifyMu   sync.Mutex
	notifyLast = map[string]time.Time{}
)

// notifyThrottled shows a notification at most once per minute per key, so a retry loop cannot spam the desktop.
func notifyThrottled(key, title, body string) {
	notifyMu.Lock()
	if time.Since(notifyLast[key]) < time.Minute {
		notifyMu.Unlock()
		return
	}
	notifyLast[key] = time.Now()
	notifyMu.Unlock()
	notify(title, body)
}

func notify(title, body string) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	defer conn.Close()
	conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").Call(
		"org.freedesktop.Notifications.Notify", 0, "Matlink", uint32(0), "matlink", title, body,
		[]string{}, map[string]dbus.Variant{}, int32(4000))
}
