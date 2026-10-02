# Matlink

Use an Android tablet as a second monitor for Fedora over USB. A tray program on the PC creates a virtual monitor, encodes it as H.264 and sends it through `adb` to the Matlink app on the tablet. Everything starts automatically when the tablet is plugged in.

Tested on Fedora 44 KDE Plasma 6 (Wayland) with a Galaxy Tab A9+ (Android 16). Other desktops are not supported yet: the virtual monitor is sized and positioned with `kscreen-doctor` (KDE).

## Setup

1. Install the dependencies on the PC (most are already present on Fedora KDE):
   ```
   sudo dnf install android-tools kscreen pipewire-gstreamer gstreamer1-plugins-bad-free gstreamer1-plugin-openh264
   ```
2. On the tablet enable **Developer options** and **USB debugging** (Settings > About tablet > tap *Build number* 7 times).
3. Download `Matlink-1.0.0-x86_64.AppImage` from the [latest release](../../releases/latest), then:
   ```
   chmod +x Matlink-*-x86_64.AppImage
   ./Matlink-*-x86_64.AppImage
   ```
4. Plug in the tablet and accept the USB debugging prompt ("Always allow"). Matlink installs the app, starts it and the tablet turns into a monitor. The APK is also attached to the release for manual install (`adb install matlink-*.apk`).
5. Optional: tray menu > Settings > *Start at login*.

## Features

- Automatic: detects the tablet, sets up the USB tunnel, installs/updates and launches the app, creates the virtual monitor, removes it when unplugged.
- Resolution matched to the tablet (capped to what its hardware H.264 decoder supports), portrait or landscape, UI scale from the tablet density.
- Hardware decoding on the tablet, low-latency H.264 over USB (no Wi-Fi needed).
- Cursor shown on the tablet; the monitor appears as a normal extra screen in System Settings > Display.
- Tray settings (PC): enable/disable, resolution (auto or fixed), frame rate, quality, UI scale, position (right/left/above/below), launch app automatically, install/update app automatically, start at login, reconnect.
- App settings (tablet, tap the screen then *Settings*): orientation, resolution cap, frame rate, quality, keep screen on, stats overlay. The PC follows the tablet request unless a PC setting overrides it.

Not included yet: touch input back to the PC, audio.

## Build

`./build.sh` builds `dist/matlink-<ver>.apk` and `dist/Matlink-<ver>-x86_64.AppImage`. Needs JDK 17+, the Android SDK (platform 35), Gradle 8.10, Go and `appimagetool`; set `MATLINK_TOOLCHAIN` to the folder that holds them (default `~/matlink-toolchain`). Set `MATLINK_KEYSTORE` and `MATLINK_KS_PASS` to sign with your own key.

## How it works

`adb reverse` maps a TCP port on the tablet to the PC. The app connects, sends its size/preferences, the PC opens an xdg-desktop-portal ScreenCast session with a virtual source, sets the mode via `kscreen-doctor`, and pipes PipeWire through `gst-launch-1.0` (OpenH264) to the app, which decodes with `MediaCodec` onto a `SurfaceView`. Config: `~/.config/matlink/config.json`, log: `~/.cache/matlink/matlink.log`.
