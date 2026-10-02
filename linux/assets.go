package main

import _ "embed"

//go:embed assets/icon.png
var iconPNG []byte

//go:embed assets/matlink.apk
var embeddedAPK []byte
