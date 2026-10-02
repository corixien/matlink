#!/usr/bin/env bash
# Builds the APK and the AppImage into ./dist
# Needs: JDK 17+, Android SDK (ANDROID_HOME), gradle, go, appimagetool.
# Optional signing: MATLINK_KEYSTORE + MATLINK_KS_PASS (otherwise debug-signed).
set -euo pipefail
cd "$(dirname "$0")"
TC="${MATLINK_TOOLCHAIN:-$HOME/matlink-toolchain}"
export JAVA_HOME="${JAVA_HOME:-$TC/jdk}" ANDROID_HOME="${ANDROID_HOME:-$TC/sdk}"
GRADLE="${GRADLE:-$TC/gradle/bin/gradle}"
APPIMAGETOOL="${APPIMAGETOOL:-$TC/appimagetool}"
[ -f "$TC/matlink.jks" ] && export MATLINK_KEYSTORE="${MATLINK_KEYSTORE:-$TC/matlink.jks}" MATLINK_KS_PASS="${MATLINK_KS_PASS:-$(cat "$TC/ks.pass")}"
VERSION=$(grep -oP 'const version = "\K[^"]+' linux/main.go)
mkdir -p dist

echo "== Android APK"
echo "sdk.dir=$ANDROID_HOME" > android/local.properties
(cd android && "$GRADLE" --no-daemon -q assembleRelease)
cp android/app/build/outputs/apk/release/app-release.apk "dist/matlink-$VERSION.apk"
cp "dist/matlink-$VERSION.apk" linux/assets/matlink.apk

echo "== Linux binary"
(cd linux && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../dist/matlink .)

echo "== AppImage"
rm -rf dist/AppDir && mkdir -p dist/AppDir/usr/bin
cp dist/matlink dist/AppDir/usr/bin/matlink
cp linux/assets/icon.png dist/AppDir/matlink.png
cp packaging/matlink.desktop dist/AppDir/matlink.desktop
printf '#!/bin/sh\nexec "$(dirname "$(readlink -f "$0")")/usr/bin/matlink" "$@"\n' > dist/AppDir/AppRun
chmod +x dist/AppDir/AppRun
ARCH=x86_64 "$APPIMAGETOOL" --appimage-extract-and-run dist/AppDir "dist/Matlink-$VERSION-x86_64.AppImage" >/dev/null
rm -rf dist/AppDir dist/matlink
ls -la dist
