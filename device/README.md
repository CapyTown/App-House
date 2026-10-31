# App House device inspector

`Inspector.java` is a read-only Android command, built into the embedded
`internal/adb/inspector.jar`. It runs as the authorized ADB shell user via
`app_process`; it does not install an APK, root the device, or change packages.

Rebuild from the repository root with JDK 17 and Android SDK build tools:

```sh
./scripts/build-inspector.sh
```

The defaults are build tools `36.1.0` and platform `android-36`. Override
`ANDROID_SDK_ROOT` (or `ANDROID_HOME`), `ANDROID_BUILD_TOOLS_VERSION`, and
`ANDROID_PLATFORM_VERSION` as needed. The generated DEX targets Android 8.0
(API 26) and newer. Java is needed only to rebuild the helper.

App House invokes it with a concrete, nonnegative Android user ID:

```sh
CLASSPATH=/data/local/tmp/androidfun-inspector.jar app_process / com.androidfun.Inspector 0
```

Standard output is UTF-8 JSON lines: one `type: app` object for each installed
package, followed by `{"type":"done","count":123}`. A fatal error emits
`{"type":"error","message":"..."}` and exits nonzero. Diagnostics go to stderr.
The desktop must require the final `done` record to distinguish completion from
a disconnected device or terminated command.

The package list includes system, updated system, and disabled apps for the
selected Android user, but excludes packages uninstalled for that user.
`permissions` lists manifest-requested permissions with PackageManager grant
flags. Those flags do not describe AppOps overrides, one-time access, or an app's
actual permission use. `signers` contains the current APK signing certificates;
`organization` is the certificate's self-declared `O` field, not a verified author
or company. It can be empty. `sha256` hashes each certificate's encoded bytes.
Icons are base64-encoded 64 × 64 PNG images. Per-package resource or certificate
failures produce `warnings` and leave the affected fields empty.

The shell context uses Android's hidden `ActivityThread` and
`createPackageContextAsUser` APIs, retaining shell attribution and the requested
user. This also supports Android 8 vendor builds without `createContextAsUser`.
These may differ on vendor builds or future Android releases. Cross-user access
can be denied by device policy; failures are reported rather than silently
falling back to a different user. Compilation does not replace testing on actual
Android versions and vendor devices.
