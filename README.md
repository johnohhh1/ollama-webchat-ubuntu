# Ollama Desktop — Linux (Ubuntu/GTK)

Native GTK3 + WebKit2GTK desktop app for [Ollama](https://ollama.com) on Linux.

This is a Linux port of the official Ollama desktop app (from the main [ollama/ollama](https://github.com/ollama/ollama) repo). The official app ships Windows and macOS binaries only. This port removes that restriction.

**Tested on:** Ubuntu 26.04 (GNOME 50, Wayland). Should work on any GTK3 desktop.

---

## What it is

- A GTK3 window containing a WebKit2GTK browser rendering Ollama's React frontend
- Talks to your existing Ollama service at `localhost:11434` — does **not** start or manage the Ollama daemon
- Full chat UI: model selection, file attachments, settings, chat history (SQLite)
- Single-instance lockfile, zenity file/folder dialogs, dark mode

## Prerequisites

### Runtime

```bash
sudo apt install libwebkit2gtk-4.1-0 libgtk-3-0 zenity
```

### Build

```bash
sudo apt install \
    libwebkit2gtk-4.1-dev libgtk-3-dev \
    golang-go nodejs npm \
    pkg-config git
```

Go 1.21+ required. Check with `go version`.

---

## Build

```bash
git clone https://github.com/johnohhh1/ollama-webchat-ubuntu.git
cd ollama-webchat-ubuntu
bash build.sh
```

The binary lands at `build/ollama-app`. Install it:

```bash
sudo cp build/ollama-app /usr/local/bin/ollama-app
```

---

## Usage

Make sure Ollama is running:

```bash
ollama serve   # or: sudo systemctl start ollama
```

Then launch the app:

```bash
ollama-app
```

Or find it in your application launcher (if you created a `.desktop` entry).

### Stale lockfile

If the app crashes without cleaning up, delete the lock before restarting:

```bash
rm -f ~/.ollama/app.lock
ollama-app
```

---

## How the port works

The upstream `app/` directory was gated with `//go:build windows || darwin` on ~40 files. The underlying C webview library (`webview.h`) already has full GTK/WebKit2GTK support — only the Go build tags blocked Linux.

Changes made:

| Type | What |
|------|------|
| Build tag | `windows \|\| darwin` → `windows \|\| darwin \|\| linux` on ~40 files |
| `app/webview/webview.go` | Added Linux CGo flags: `WEBVIEW_GTK`, `gtk+-3.0`, `webkit2gtk-4.1` |
| `app/cmd/app/app_linux.go` | GTK window management, lockfile single-instance, signal cleanup |
| `app/cmd/app/webview_linux.go` | Webview struct + JS bindings (show/hide/drag/zoom/file picker) |
| `app/cmd/app/filepicker_linux.go` | zenity directory and multi-file choosers |
| `app/server/server_linux.go` | Health-check only — never spawns a process, uses existing systemd service |
| `app/updater/updater_linux.go` | All no-ops — no auto-update on Linux |
| `app/dialog/dlgs_linux.go` | zenity dialogs (yes/no, info, error, file open/save) |
| `app/ui/app/src/components/ChatSidebar.tsx` | Settings link was `isWindows`-gated; fixed to show on Linux too |

---

## EGL warnings on startup

On NVIDIA proprietary drivers you'll see Mesa EGL warnings — these are harmless. WebKit falls back to Cairo software rendering for compositing. For a chat UI this is imperceptible.

---

## License

The Ollama desktop app source is MIT licensed (see [ollama/ollama](https://github.com/ollama/ollama)). The Linux port files in this repo are also MIT.
