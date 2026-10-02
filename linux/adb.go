package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	appPackage     = "com.matlink.display"
	appActivity    = appPackage + "/.MainActivity"
	appVersionCode = 2 // keep in sync with android/app/build.gradle
)

type DeviceEvent struct {
	Serial string
	State  string // device, unauthorized, offline, ...
}

func adb(args ...string) (string, error) {
	out, err := exec.Command("adb", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// trackDevices streams adb device state changes. It reconnects forever.
func trackDevices(ch chan<- map[string]string) {
	for {
		exec.Command("adb", "start-server").Run()
		c, err := net.Dial("tcp", "127.0.0.1:5037")
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		req := "host:track-devices"
		fmt.Fprintf(c, "%04x%s", len(req), req)
		r := bufio.NewReader(c)
		ok := make([]byte, 4)
		if _, err := io.ReadFull(r, ok); err != nil || string(ok) != "OKAY" {
			c.Close()
			time.Sleep(3 * time.Second)
			continue
		}
		for {
			lenb := make([]byte, 4)
			if _, err := io.ReadFull(r, lenb); err != nil {
				break
			}
			n, _ := strconv.ParseInt(string(lenb), 16, 32)
			payload := make([]byte, n)
			if _, err := io.ReadFull(r, payload); err != nil {
				break
			}
			devs := map[string]string{}
			for _, ln := range strings.Split(string(payload), "\n") {
				if f := strings.Fields(ln); len(f) == 2 {
					devs[f[0]] = f[1]
				}
			}
			ch <- devs
		}
		c.Close()
		time.Sleep(2 * time.Second)
	}
}

func installedVersion(serial string) int {
	out, err := adb("-s", serial, "shell", "dumpsys package "+appPackage+" | grep versionCode")
	if err != nil || out == "" {
		return 0
	}
	for _, f := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(f, "versionCode="); ok {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

func deviceModel(serial string) string {
	out, _ := adb("-s", serial, "shell", "getprop ro.product.model")
	if out == "" {
		return serial
	}
	return out
}

// prepareDevice sets up the reverse tunnel, installs the app if needed and launches it.
func prepareDevice(serial string, port int, conf Config) error {
	if out, err := adb("-s", serial, "reverse", fmt.Sprintf("tcp:%d", port), fmt.Sprintf("tcp:%d", port)); err != nil {
		return fmt.Errorf("adb reverse: %s", out)
	}
	if conf.AutoInstall && len(embeddedAPK) > 1000 {
		if v := installedVersion(serial); v < appVersionCode {
			dir, _ := os.UserCacheDir()
			p := filepath.Join(dir, "matlink", "matlink.apk")
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, embeddedAPK, 0o644); err != nil {
				return err
			}
			notify("Matlink", "Installing the Matlink app on the tablet. Confirm on the tablet if asked.")
			if out, err := adb("-s", serial, "install", "-r", p); err != nil {
				return fmt.Errorf("install failed: %s", out)
			}
		}
	}
	if conf.AutoLaunch {
		adb("-s", serial, "shell", "input keyevent KEYCODE_WAKEUP")
		if out, err := adb("-s", serial, "shell", "am", "start", "-n", appActivity,
			"--ez", "autostart", "true", "-f", "0x10000000"); err != nil {
			log.Printf("launch: %s", out)
			return fmt.Errorf("could not launch app (installed?): %s", out)
		}
	}
	return nil
}
