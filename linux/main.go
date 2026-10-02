package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/systray"
)

const version = "1.0.0"

var (
	mStatus *systray.MenuItem
	stateMu sync.Mutex
	devices = map[string]string{} // serial -> adb state
	session string
)

func setStatus() {
	stateMu.Lock()
	defer stateMu.Unlock()
	text := "Waiting for tablet (USB debugging on)"
	for serial, st := range devices {
		switch st {
		case "device":
			text = "Tablet connected: " + deviceNames[serial]
		case "unauthorized":
			text = "Tablet: accept the USB debugging prompt"
		}
	}
	if session != "" {
		text = session
	}
	if !getConfig().Enabled {
		text = "Disabled"
	}
	mStatus.SetTitle(text)
	systray.SetTooltip("Matlink - " + text)
}

var deviceNames = map[string]string{}

func main() {
	logDir, _ := os.UserCacheDir()
	os.MkdirAll(filepath.Join(logDir, "matlink"), 0o755)
	if f, err := os.OpenFile(filepath.Join(logDir, "matlink", "matlink.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f)
	}
	loadConfig()
	systray.Run(onReady, func() {})
}

func onReady() {
	systray.SetIcon(iconPNG)
	systray.SetTitle("Matlink")
	mStatus = systray.AddMenuItem("Starting...", "")
	mStatus.Disable()

	srv := &Server{OnState: func(st string) {
		stateMu.Lock()
		session = st
		stateMu.Unlock()
		setStatus()
	}}
	if err := srv.Start(getConfig().Port); err != nil {
		notify("Matlink", fmt.Sprintf("Port %d is busy - is Matlink already running?", getConfig().Port))
		log.Printf("listen: %v", err)
		systray.Quit()
		return
	}

	for _, dep := range []struct{ bin, pkg string }{{"adb", "android-tools"}, {"gst-launch-1.0", "gstreamer1-plugins-bad-free gstreamer1-plugin-openh264 pipewire-gstreamer"}, {"kscreen-doctor", "kscreen"}} {
		if _, err := exec.LookPath(dep.bin); err != nil {
			notify("Matlink", fmt.Sprintf("Missing %s. Run: sudo dnf install %s", dep.bin, dep.pkg))
			log.Printf("missing dependency %s", dep.bin)
		}
	}

	systray.AddSeparator()
	mEnabled := systray.AddMenuItemCheckbox("Enabled", "Automatically use connected tablets", getConfig().Enabled)
	go func() {
		for range mEnabled.ClickedCh {
			updateConfig(func(c *Config) { c.Enabled = !c.Enabled })
			if getConfig().Enabled {
				mEnabled.Check()
			} else {
				mEnabled.Uncheck()
				srv.Stop()
			}
			setStatus()
		}
	}()

	mSet := systray.AddMenuItem("Settings", "")
	radio(mSet, "Resolution", []opt{{"Auto (match tablet)", "auto"}, {"1920x1200", "1920x1200"}, {"1920x1080", "1920x1080"},
		{"1600x1000", "1600x1000"}, {"1280x800", "1280x800"}, {"1280x720", "1280x720"}},
		func(c *Config) *string { return &c.Resolution })
	radioInt(mSet, "Frame rate", []optInt{{"Auto (tablet)", 0}, {"30 fps", 30}, {"60 fps", 60}}, func(c *Config) *int { return &c.FPS })
	radio(mSet, "Quality", []opt{{"Auto (tablet)", "auto"}, {"Low", "low"}, {"Medium", "medium"}, {"High", "high"}, {"Ultra", "ultra"}},
		func(c *Config) *string { return &c.Quality })
	radio(mSet, "UI scale", []opt{{"Auto", "auto"}, {"100%", "1"}, {"125%", "1.25"}, {"150%", "1.5"}, {"175%", "1.75"}, {"200%", "2"}},
		func(c *Config) *string { return &c.Scale })
	radio(mSet, "Position", []opt{{"Right of screens", "right"}, {"Left of screens", "left"}, {"Above", "above"}, {"Below", "below"}},
		func(c *Config) *string { return &c.Position })
	toggle(mSet, "Launch app on tablet automatically", func(c *Config) *bool { return &c.AutoLaunch })
	toggle(mSet, "Install/update app on tablet automatically", func(c *Config) *bool { return &c.AutoInstall })
	mLogin := mSet.AddSubMenuItemCheckbox("Start at login", "", autostartEnabled())
	go func() {
		for range mLogin.ClickedCh {
			on := !autostartEnabled()
			setAutostart(on)
			if on {
				mLogin.Check()
			} else {
				mLogin.Uncheck()
			}
		}
	}()

	mReconnect := systray.AddMenuItem("Reconnect tablet", "")
	go func() {
		for range mReconnect.ClickedCh {
			srv.Stop()
			for serial, st := range snapshotDevices() {
				if st == "device" {
					go connectDevice(serial)
				}
			}
		}
	}()
	systray.AddSeparator()
	mAbout := systray.AddMenuItem("Matlink "+version, "")
	mAbout.Disable()
	mQuit := systray.AddMenuItem("Quit", "")
	go func() { <-mQuit.ClickedCh; srv.Stop(); systray.Quit() }()
	setStatus()

	// device tracking
	ch := make(chan map[string]string, 4)
	go trackDevices(ch)
	go func() {
		known := map[string]string{}
		for devs := range ch {
			for serial, st := range devs {
				if known[serial] != st && st == "device" {
					go connectDevice(serial)
				}
				if known[serial] != st && st == "unauthorized" {
					notify("Matlink", "Tablet found: accept the USB debugging prompt on its screen.")
				}
			}
			for serial := range known {
				if _, ok := devs[serial]; !ok {
					srv.Stop()
				}
			}
			known = devs
			stateMu.Lock()
			devices = devs
			stateMu.Unlock()
			setStatus()
		}
	}()
}

func snapshotDevices() map[string]string {
	stateMu.Lock()
	defer stateMu.Unlock()
	m := map[string]string{}
	for k, v := range devices {
		m[k] = v
	}
	return m
}

func connectDevice(serial string) {
	conf := getConfig()
	if !conf.Enabled {
		return
	}
	stateMu.Lock()
	deviceNames[serial] = deviceModel(serial)
	stateMu.Unlock()
	setStatus()
	if err := prepareDevice(serial, conf.Port, conf); err != nil {
		log.Printf("prepare %s: %v", serial, err)
		notify("Matlink", err.Error())
	}
}

type opt struct{ label, val string }
type optInt struct {
	label string
	val   int
}

func radio(parent *systray.MenuItem, title string, opts []opt, field func(*Config) *string) {
	sub := parent.AddSubMenuItem(title, "")
	cur := *field(&cfg)
	items := make([]*systray.MenuItem, len(opts))
	for i, o := range opts {
		items[i] = sub.AddSubMenuItemCheckbox(o.label, "", o.val == cur)
		go func(i int, o opt) {
			for range items[i].ClickedCh {
				updateConfig(func(c *Config) { *field(c) = o.val })
				for j := range items {
					if j == i {
						items[j].Check()
					} else {
						items[j].Uncheck()
					}
				}
			}
		}(i, o)
	}
}

func radioInt(parent *systray.MenuItem, title string, opts []optInt, field func(*Config) *int) {
	sub := parent.AddSubMenuItem(title, "")
	cur := *field(&cfg)
	items := make([]*systray.MenuItem, len(opts))
	for i, o := range opts {
		items[i] = sub.AddSubMenuItemCheckbox(o.label, "", o.val == cur)
		go func(i int, o optInt) {
			for range items[i].ClickedCh {
				updateConfig(func(c *Config) { *field(c) = o.val })
				for j := range items {
					if j == i {
						items[j].Check()
					} else {
						items[j].Uncheck()
					}
				}
			}
		}(i, o)
	}
}

func toggle(parent *systray.MenuItem, title string, field func(*Config) *bool) {
	it := parent.AddSubMenuItemCheckbox(title, "", *field(&cfg))
	go func() {
		for range it.ClickedCh {
			updateConfig(func(c *Config) { *field(c) = !*field(c) })
			if *field(&cfg) {
				it.Check()
			} else {
				it.Uncheck()
			}
		}
	}()
}

func autostartPath() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "autostart", "matlink.desktop")
}

func autostartEnabled() bool {
	_, err := os.Stat(autostartPath())
	return err == nil
}

func setAutostart(on bool) {
	if !on {
		os.Remove(autostartPath())
		return
	}
	exe := os.Getenv("APPIMAGE")
	if exe == "" {
		exe, _ = os.Executable()
	}
	os.MkdirAll(filepath.Dir(autostartPath()), 0o755)
	body := "[Desktop Entry]\nType=Application\nName=Matlink\nComment=Use an Android tablet as a USB second monitor\nExec=\"" +
		strings.ReplaceAll(exe, "\"", "\\\"") + "\"\nIcon=matlink\nX-GNOME-Autostart-enabled=true\n"
	os.WriteFile(autostartPath(), []byte(body), 0o644)
}
