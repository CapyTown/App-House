#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "$0")/.." && pwd)"
sdk_dir="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-$HOME/Library/Android/sdk}}"
build_tools_version="${ANDROID_BUILD_TOOLS_VERSION:-36.1.0}"
platform_version="${ANDROID_PLATFORM_VERSION:-android-36}"
android_jar="$sdk_dir/platforms/$platform_version/android.jar"
d8="$sdk_dir/build-tools/$build_tools_version/d8"

if [[ ! -f "$android_jar" || ! -x "$d8" ]]; then
    echo "Android SDK not found: set ANDROID_SDK_ROOT, ANDROID_BUILD_TOOLS_VERSION, and ANDROID_PLATFORM_VERSION." >&2
    exit 1
fi
if [[ -z "${JAVA_HOME:-}" && -x /usr/libexec/java_home ]]; then
    export JAVA_HOME="$(/usr/libexec/java_home -v 17)"
fi
javac="${JAVA_HOME:+$JAVA_HOME/bin/}javac"
jar="${JAVA_HOME:+$JAVA_HOME/bin/}jar"
build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT
mkdir -p "$build_dir/classes" "$build_dir/dex" "$project_dir/internal/adb"

"$javac" --release 8 -encoding UTF-8 -classpath "$android_jar" \
    -d "$build_dir/classes" "$project_dir/device/Inspector.java" "$project_dir/device/DistinguishedName.java"
"$jar" cf "$build_dir/classes.jar" -C "$build_dir/classes" .
"$d8" --release --min-api 26 --lib "$android_jar" \
    --output "$build_dir/dex" "$build_dir/classes.jar"
"$jar" --create --file "$project_dir/internal/adb/inspector.jar" \
    --no-manifest --date=2020-01-01T00:00:00Z -C "$build_dir/dex" classes.dex
echo "Built internal/adb/inspector.jar (Android 8.0+)"
