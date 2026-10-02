package main

import "github.com/godbus/dbus/v5"

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
