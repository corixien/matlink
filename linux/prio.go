package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

// boostPriority moves this process into its own transient systemd scope with a high CPU weight
// (1000, ordinary applications have 100). Under CPU load the scheduler then gives capture, encode and
// relay about ten times the share of any normal app, so frames no longer queue behind desktop work,
// which was the main reason latency grew when the laptop was busy. Child processes (gst-launch,
// ffmpeg) inherit the cgroup. Needs the systemd user manager and its cpu controller; failures are ignored.
func boostPriority() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	defer conn.Close()
	type prop struct {
		Name  string
		Value dbus.Variant
	}
	props := []prop{
		{"Description", dbus.MakeVariant("Matlink streaming")},
		{"PIDs", dbus.MakeVariant([]uint32{uint32(os.Getpid())})},
		{"CPUWeight", dbus.MakeVariant(uint64(1000))},
	}
	var job dbus.ObjectPath
	err = conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1").Call(
		"org.freedesktop.systemd1.Manager.StartTransientUnit", 0,
		fmt.Sprintf("matlink-%d.scope", os.Getpid()), "fail", props, []struct {
			Name  string
			Props []prop
		}{}).Store(&job)
	if err != nil {
		log.Printf("priority boost unavailable: %v", err)
		return
	}
	time.Sleep(200 * time.Millisecond)
	cg, _ := os.ReadFile("/proc/self/cgroup")
	log.Printf("priority: CPU weight 1000, cgroup %s", string(cg[:max(len(cg)-1, 0)]))
}
