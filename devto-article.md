---
title: I Ported the Ollama Desktop App to Linux Just in Time for Ubuntu 26.04 LTS
published: true
tags: linux, ubuntu, ollama, go
cover_image: https://raw.githubusercontent.com/johnohhh1/ollama-webchat-ubuntu/main/assets/Ohhhllama.png
---

Ubuntu 26.04 LTS drops this week. I've been running the RC for months. And I think it's going to be the release that finally pulls a meaningful chunk of people off Windows — especially AI-curious developers who are tired of the telemetry, the forced updates, and the general feeling that Microsoft is treating them like a product.

So I've been on a mission: take the cool AI tooling that still assumes Windows or macOS, and make it work natively on Linux.

This week's project: **the official Ollama desktop app**.

---

## The Problem

[Ollama](https://ollama.com) is incredible. Local LLMs, dead simple, fast. But their desktop app — the polished GTK-style chat UI with model management, file attachments, settings, chat history — **officially ships for Windows and macOS only**.

Every single Go file in the `app/` directory of [ollama/ollama](https://github.com/ollama/ollama) starts with:

```go
//go:build windows || darwin
```

Linux users are left running `ollama serve` and pointing a browser at `localhost:11434`. It works, but it's not the experience.

---

## The Discovery

Before writing a single line of code, I did what any good engineer does: I read the source.

The Ollama desktop app is built on [webview](https://github.com/webview/webview) — a lightweight C++ library that wraps the platform's native browser engine. On Windows it uses WebView2 (Edge). On macOS it uses WebKit. And on Linux?

```cpp
// From webview.h, line ~400:
#if defined(WEBVIEW_GTK)
// Full GTK3 + WebKit2GTK implementation
```

**The Linux support was already there.** Fully implemented. Sitting in the header file, waiting. The Go build tags were the only thing blocking it.

---

## The Port

Six new files. A patch. About 350 lines of Go.

### 1. Unlock the build tags

~40 files needed one line changed:

```go
// before
//go:build windows || darwin

// after  
//go:build windows || darwin || linux
```

### 2. Wire up the CGo flags

`app/webview/webview.go` needed Linux-specific build flags to find GTK and WebKit:

```go
#cgo linux CXXFLAGS: -DWEBVIEW_GTK -std=c++14
#cgo linux LDFLAGS: -ldl
#cgo linux pkg-config: gtk+-3.0 webkit2gtk-4.1
```

Note: Ubuntu 26.04 ships `webkit2gtk-4.1` — not `4.0`. If you're targeting older distros, check your version.

### 3. Platform files

The app architecture expects platform-specific implementations of a handful of interfaces. I wrote six `_linux.go` files:

**`app_linux.go`** — GTK window management via CGo, lockfile-based single-instance enforcement, signal cleanup:

```go
func handleExistingInstance(startHidden bool) {
    f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
    if err != nil {
        slog.Info("another ollama-app instance detected, exiting")
        os.Exit(0)
    }
    f.Close()
    go func() {
        c := make(chan os.Signal, 1)
        signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
        <-c
        removeLock()
    }()
}
```

**`server_linux.go`** — The app normally spawns the Ollama daemon itself. On Linux, systemd handles that. So this file is just a health-checker that pings `localhost:11434` every 30 seconds and logs if it's unreachable. Never spawns a process.

**`updater_linux.go`** — All no-ops. systemd and apt manage updates on Linux.

**`filepicker_linux.go`** — File/folder dialogs via `zenity`:

```go
func selectDirectory(title string) (string, error) {
    out, err := exec.Command("zenity", "--file-selection", "--directory", "--title="+title).Output()
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(out)), nil
}
```

**`dialog/dlgs_linux.go`** — zenity implementations of the app's dialog interface (yes/no, info, error, file pickers).

**`webview_linux.go`** — The Webview struct without the Windows `menu.h` CGo baggage, plus all the JS bindings: `ready`, `close`, `drag`, `doubleClick`, `zoomIn`, `zoomOut`, `selectFiles`, `selectWorkingDirectory`.

### 4. The one frontend bug

The React sidebar had:

```tsx
{isWindows && (
  <Link href="/settings">Settings</Link>
)}
```

On Linux, `navigator.platform` returns `"Linux x86_64"` — so `isWindows` is false and Settings just... doesn't appear. One-line fix:

```tsx
const isMac = navigator.platform.toLowerCase().includes("mac");

{!isMac && (
  <Link href="/settings">Settings</Link>
)}
```

---

## The Result

```bash
git clone https://github.com/johnohhh1/ollama-webchat-ubuntu.git
cd ollama-webchat-ubuntu
bash build.sh
sudo cp build/ollama-app /usr/local/bin/ollama-app
ollama-app
```

A native GTK3 window. WebKit2GTK rendering the full React SPA. Chat history in SQLite. File attachments. Model management. Settings. Dark mode. Wayland-native — no XWayland needed.

---

## The NVIDIA gotcha

Running an RTX 5070 (Blackwell) with the proprietary driver? You'll see a wall of EGL warnings on startup:

```
libEGL warning: egl: failed to create dri2 screen
libEGL warning: pci id for fd 17: 10de:2f04, driver (null)
```

**Ignore them.** There's no open-source Mesa driver for Blackwell yet — WebKit probes Mesa EGL, fails, and falls back to Cairo software rendering for the compositor. For a chat UI it's completely imperceptible. Page layout and text were always CPU-rendered anyway.

---

## Why Ubuntu 26.04 changes things

26.04 LTS is the first Ubuntu release with X11 removed entirely — Wayland only. GNOME 50. The desktop finally feels like a first-class target, not an afterthought.

At the same time, the local AI tooling ecosystem has exploded. Ollama, LM Studio, Open WebUI, llama.cpp — these tools are genuinely good, and they mostly work on Linux. The desktop app experience is just lagging behind.

That's the gap I'm trying to close. If you're a developer making the jump from Windows to Ubuntu 26.04 this week, you shouldn't have to give up your tools.

---

## Repo

**[github.com/johnohhh1/ollama-webchat-ubuntu](https://github.com/johnohhh1/ollama-webchat-ubuntu)**

PRs welcome — especially if you test on Fedora, Arch, or Ubuntu 24.04. And if you're building other AI tools that are stuck behind Windows/macOS build tags, the pattern here is reusable.

The C library usually already supports Linux. The build tags are almost always the only thing in the way.
