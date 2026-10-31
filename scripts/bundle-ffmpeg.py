#!/usr/bin/env python3
"""Stage checksum-pinned FFmpeg helpers for the embedded screen mirror."""
import argparse
import gzip
import hashlib
import io
import json
import os
import platform as host_platform
from pathlib import Path
import shutil
import tempfile
import subprocess
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
RELEASE = "b6.1.1"
BASE_URL = f"https://github.com/eugeneware/ffmpeg-static/releases/download/{RELEASE}/"
MAX_BYTES = 200 * 1024 * 1024
# SHA-256 values published on the pinned GitHub release's asset list.
ASSETS = {
    "win32-x64": {
        "binary": "8883a3dffbd0a16cf4ef95206ea05283f78908dbfb118f73c83f4951dcc06d77",
        "LICENSE": "8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903",
        "README": "a636a7183c58006351acbaf35303c0ed85c6e1320fd4e80de453ba6157de6311",
        "builder": "https://www.gyan.dev/ffmpeg/builds/",
    },
}
SOURCE_VERSION = "9.0.2"
SOURCE_NAME = f"ffmpeg-{SOURCE_VERSION}.tar.xz"
SOURCE_URL = f"https://ffmpeg.org/releases/{SOURCE_NAME}"
# Verified against FFmpeg's signed release archive. Signing key fingerprint:
# FCF986EA15E6E293A5644F10B4322F04D67658D8
SOURCE_SHA256 = "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e"


def read_asset(name, digest, local_directory, url=None):
    if local_directory:
        stream = (local_directory / name).open("rb")
    else:
        stream = urllib.request.urlopen(url or BASE_URL + name, timeout=90)
    with stream:
        data = stream.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES:
        raise ValueError(f"Asset is unexpectedly large: {name}")
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError(f"Asset SHA-256 mismatch: {name}")
    return data


def stage_windows(local_directory):
    parent = ROOT / "resources" / "windows"
    parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".ffmpeg-", dir=parent) as temporary:
        staged = Path(temporary) / "ffmpeg"
        staged.mkdir()
        gpl = read_asset("win32-x64.LICENSE", ASSETS["win32-x64"]["LICENSE"], local_directory)
        asset = "win32-x64"
        hashes = ASSETS[asset]
        filename = f"ffmpeg-{asset}.gz"
        data = read_asset(filename, hashes["binary"], local_directory)
        with gzip.GzipFile(fileobj=io.BytesIO(data)) as compressed:
            binary = compressed.read(MAX_BYTES + 1)
        if len(binary) > MAX_BYTES:
            raise ValueError(f"Expanded executable is unexpectedly large: {filename}")
        executable = staged / "ffmpeg.exe"
        executable.write_bytes(binary)
        executable.chmod(0o755)
        for kind in ("LICENSE", "README"):
            (staged / f"{kind}.txt").write_bytes(read_asset(f"{asset}.{kind}", hashes[kind], local_directory))
        (staged / "COPYING.GPLv3").write_bytes(gpl)
        origin = {
            "release": RELEASE,
            "architecture": "amd64",
            "source": BASE_URL + filename,
            "archiveSHA256": hashes["binary"],
            "executableSHA256": hashlib.sha256(binary).hexdigest(),
            "builder": hashes["builder"],
            "buildRecipe": f"https://github.com/eugeneware/ffmpeg-static/blob/{RELEASE}/download-binaries/index.sh",
            "sourceCode": "https://ffmpeg.org/releases/ffmpeg-6.1.1.tar.xz",
        }
        (staged / "origin.json").write_text(json.dumps(origin, indent=2) + "\n", encoding="utf-8")
        (staged / "SOURCE.txt").write_text(
            "FFmpeg is a separate executable distributed under its own license.\n"
            "See LICENSE.txt, COPYING.GPLv3 and README.txt for upstream notices and build details.\n"
            f"Binary distribution: https://github.com/eugeneware/ffmpeg-static/releases/tag/{RELEASE}\n"
            f"Original binary builder: {hashes['builder']}\n"
            f"Build recipe: {origin['buildRecipe']}\n"
            f"FFmpeg source release: {origin['sourceCode']}\n"
            "Additional enabled library versions and source information are documented by the original builder.\n",
            encoding="utf-8")
        destination = parent / "ffmpeg"
        if destination.exists():
            shutil.rmtree(destination)
        staged.rename(destination)
    print(f"Staged FFmpeg ({RELEASE}) in {destination}")


def stage_macos(local_directory, jobs):
    if host_platform.system() != "Darwin":
        raise ValueError("The macOS FFmpeg helper must be built on macOS with Xcode command-line tools")
    parent = ROOT / "resources" / "darwin"
    parent.mkdir(parents=True, exist_ok=True)
    data = read_asset(SOURCE_NAME, SOURCE_SHA256, local_directory, SOURCE_URL)
    with tempfile.TemporaryDirectory(prefix=".ffmpeg-", dir=parent) as temporary:
        temporary = Path(temporary)
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:xz") as archive:
            members = archive.getmembers()
            if sum(entry.size for entry in members) > MAX_BYTES:
                raise ValueError("Expanded FFmpeg source is unexpectedly large")
            for entry in members:
                path = Path(entry.name)
                if (path.is_absolute() or ".." in path.parts or not path.parts
                        or path.parts[0] != f"ffmpeg-{SOURCE_VERSION}"
                        or not (entry.isfile() or entry.isdir())):
                    raise ValueError(f"Unsafe FFmpeg source entry: {entry.name}")
            archive.extractall(temporary, filter="data")
        source = temporary / f"ffmpeg-{SOURCE_VERSION}"
        staged = temporary / "ffmpeg"
        staged.mkdir()
        for architecture, compiler_arch, ffmpeg_arch in (("arm64", "arm64", "aarch64"), ("amd64", "x86_64", "x86_64")):
            build = temporary / f"build-{architecture}"
            build.mkdir()
            configure = [
                str(source / "configure"), "--target-os=darwin", f"--arch={ffmpeg_arch}",
                f"--cc=clang -arch {compiler_arch}", "--enable-cross-compile",
                "--disable-autodetect", "--disable-everything", "--disable-programs", "--enable-ffmpeg",
                "--disable-doc", "--disable-debug", "--disable-shared", "--enable-static",
                "--disable-gpl", "--disable-nonfree", "--disable-version3", "--disable-network",
                "--disable-x86asm", "--disable-avdevice", "--disable-swresample",
                "--enable-decoder=h264", "--enable-parser=h264", "--enable-demuxer=matroska",
                "--enable-encoder=mjpeg", "--enable-muxer=image2pipe", "--enable-protocol=file,pipe",
                "--enable-filter=scale,format", "--extra-cflags=-mmacosx-version-min=11.0",
                "--extra-ldflags=-mmacosx-version-min=11.0",
            ]
            subprocess.run(configure, cwd=build, check=True)
            subprocess.run(["make", "-s", f"-j{jobs}", "ffmpeg"], cwd=build, check=True)
            destination = staged / architecture
            destination.mkdir()
            shutil.copy2(build / "ffmpeg", destination / "ffmpeg")
            for name in ("COPYING.LGPLv2.1", "LICENSE.md", "README.md"):
                shutil.copy2(source / name, destination / name)
            # Ship the exact complete source alongside the unmodified helper.
            # This also makes the minimal build reproducible without a network.
            (destination / SOURCE_NAME).write_bytes(data)
            (destination / "BUILD.json").write_text(json.dumps({
                "configure": ["./configure", *configure[1:]], "build": ["make", "ffmpeg"],
            }, indent=2) + "\n", encoding="utf-8")
            (destination / "origin.json").write_text(json.dumps({
                "version": SOURCE_VERSION, "architecture": architecture, "source": SOURCE_URL,
                "archiveSHA256": SOURCE_SHA256, "license": "LGPL-2.1-or-later",
                "executableSHA256": hashlib.sha256((destination / "ffmpeg").read_bytes()).hexdigest(),
            }, indent=2) + "\n", encoding="utf-8")
            (destination / "SOURCE.txt").write_text(
                f"FFmpeg {SOURCE_VERSION}, built from unmodified official source.\n"
                f"Source archive: {SOURCE_NAME} (included beside this file)\n"
                f"Upstream URL: {SOURCE_URL}\nSHA-256: {SOURCE_SHA256}\n"
                "License: LGPL version 2.1 or later; see COPYING.LGPLv2.1 and LICENSE.md.\n"
                "Build instructions: extract the included archive, then run the configure and make commands in BUILD.json.\n"
                "Gated to H.264/Matroska decoding and MJPEG frames; GPL, nonfree, external autodetection and network disabled.\n",
                encoding="utf-8")
        destination = parent / "ffmpeg"
        if destination.exists():
            shutil.rmtree(destination)
        staged.rename(destination)
    print(f"Staged minimal FFmpeg {SOURCE_VERSION} in {destination}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("platform", choices=("darwin", "windows"))
    parser.add_argument("--from-directory", type=Path,
                        help="Read the pinned source archive (macOS) or .gz binary and LICENSE/README assets (Windows) from a local directory")
    parser.add_argument("--jobs", type=int, default=min(os.cpu_count() or 2, 8), help="Parallel compiler jobs for macOS")
    args = parser.parse_args()
    if args.jobs < 1:
        parser.error("--jobs must be positive")
    if args.platform == "darwin":
        stage_macos(args.from_directory, args.jobs)
    else:
        stage_windows(args.from_directory)


if __name__ == "__main__":
    main()
