package main

import _ "embed"

//go:embed assets/tray.png
var iconPNG []byte

//go:embed assets/matlink.apk
var embeddedAPK []byte
