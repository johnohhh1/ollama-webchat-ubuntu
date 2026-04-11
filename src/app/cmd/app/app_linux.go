//go:build linux

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <glib.h>
#include <stdlib.h>

static void ollama_show_window(void* window) {
	GtkWidget* w = GTK_WIDGET(window);
	gtk_widget_show_all(w);
	gtk_window_present(GTK_WINDOW(w));
}

static void ollama_hide_window(void* window) {
	gtk_widget_hide(GTK_WIDGET(window));
}

static void ollama_drag_window(void* window) {
	GdkDisplay* display = gdk_display_get_default();
	if (!display) return;
	GdkSeat* seat = gdk_display_get_default_seat(display);
	if (!seat) return;
	GdkDevice* pointer = gdk_seat_get_pointer(seat);
	if (!pointer) return;

	GdkWindow* gdk_window = gtk_widget_get_window(GTK_WIDGET(window));
	if (!gdk_window) return;

	gint x, y;
	gdk_window_get_device_position(gdk_window, pointer, &x, &y, NULL);
	gtk_window_begin_move_drag(GTK_WINDOW(window), 1, x, y, GDK_CURRENT_TIME);
}
*/
import "C"

import (
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/ollama/ollama/app/version"
)

var (
	appLogPath     = filepath.Join(os.Getenv("HOME"), ".ollama", "logs", "app.log")
	DesktopAppName = "ollama-app"
)

func logStartup() {
	slog.Info("starting Ollama desktop", "version", version.Version, "os", "linux")
}

// maybeMoveAndRestart is macOS-specific (app bundle relocation).
// On Linux the binary is already in the right place.
func maybeMoveAndRestart() appMove {
	return AlreadyMoved
}

var lockPath = filepath.Join(os.Getenv("HOME"), ".ollama", "app.lock")

// handleExistingInstance on Linux: check for a lockfile and exit if another
// instance already owns it. Registers cleanup on SIGINT/SIGTERM.
func handleExistingInstance(startHidden bool) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return
	}
	// Try to create the lock file exclusively.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		// Another instance is running — just exit.
		slog.Info("another ollama-app instance detected, exiting")
		os.Exit(0)
	}
	f.Close()
	// Ensure the lock is removed when the process exits.
	// quit() also calls removeLock(); this handles crashes via atexit equivalent.
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		<-c
		removeLock()
	}()
}

func removeLock() {
	os.Remove(lockPath) //nolint:errcheck
}

// installSymlink is macOS-specific; no-op on Linux.
func installSymlink() {}

// UpdateAvailable shows a desktop notification when an update is ready.
// On Linux we don't have a tray, so we log and optionally call notify-send.
func UpdateAvailable(_ string) error {
	slog.Info("update available (no auto-update on Linux)")
	return nil
}

// osRun is the platform main loop. On Linux we run the webview directly.
func osRun(cancel func(), hasCompletedFirstRun, startHidden bool) {
	if !startHidden {
		wv.Run("/")
	}
	// wv.Run("/") blocks until the window is closed (webview event loop).
	// When it returns the user closed the window via the GTK X button;
	// clean up the lockfile so the app can be relaunched.
	removeLock()
	cancel()
}

// quit terminates the webview event loop and removes the instance lock.
func quit() {
	removeLock()
	wv.Terminate()
}

// LaunchNewApp is a no-op on Linux (no self-updating binary swap).
func LaunchNewApp() {}

// runInBackground is a no-op on Linux (no "start in background" flow).
func runInBackground() {}

// checkAndHandleExistingInstance is Windows-specific URL-scheme handling.
func checkAndHandleExistingInstance(_ string) bool { return false }

// showWindow brings the GTK window to the foreground.
func showWindow(ptr unsafe.Pointer) {
	if ptr != nil {
		C.ollama_show_window(ptr)
	}
}

// hideWindow hides the GTK window without destroying it.
func hideWindow(ptr unsafe.Pointer) {
	if ptr != nil {
		C.ollama_hide_window(ptr)
	}
}

// drag initiates a window move-drag on Linux.
func drag(ptr unsafe.Pointer) {
	if ptr != nil {
		C.ollama_drag_window(ptr)
	}
}

// doubleClick on the title bar toggles maximize on Linux.
func doubleClick(ptr unsafe.Pointer) {
	if ptr != nil {
		w := (*C.GtkWindow)(ptr)
		if C.gtk_window_is_maximized(w) != 0 {
			C.gtk_window_unmaximize(w)
		} else {
			C.gtk_window_maximize(w)
		}
	}
}
