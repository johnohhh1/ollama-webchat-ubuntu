//go:build linux

package main

/*
#cgo pkg-config: gtk+-3.0 ayatana-appindicator3-0.1
#include <libayatana-appindicator/app-indicator.h>
#include <gtk/gtk.h>
#include <stdlib.h>

static AppIndicator *g_indicator  = NULL;
static GtkWidget   *g_status_item = NULL;

void go_tray_open(void);
void go_tray_quit(void);

static void cb_open(GtkMenuItem *item, gpointer d) { go_tray_open(); }
static void cb_quit(GtkMenuItem *item, gpointer d) { go_tray_quit(); }

static void tray_init(const char *icon_name) {
	g_indicator = app_indicator_new("ollama-app", icon_name,
		APP_INDICATOR_CATEGORY_APPLICATION_STATUS);

	GtkWidget *menu = gtk_menu_new();

	// Status label (non-interactive)
	g_status_item = gtk_menu_item_new_with_label("Ollama: checking…");
	gtk_widget_set_sensitive(g_status_item, FALSE);
	gtk_menu_shell_append(GTK_MENU_SHELL(menu), g_status_item);

	gtk_menu_shell_append(GTK_MENU_SHELL(menu), gtk_separator_menu_item_new());

	GtkWidget *open_item = gtk_menu_item_new_with_label("Open Ollama");
	g_signal_connect(open_item, "activate", G_CALLBACK(cb_open), NULL);
	gtk_menu_shell_append(GTK_MENU_SHELL(menu), open_item);

	gtk_menu_shell_append(GTK_MENU_SHELL(menu), gtk_separator_menu_item_new());

	GtkWidget *quit_item = gtk_menu_item_new_with_label("Quit");
	g_signal_connect(quit_item, "activate", G_CALLBACK(cb_quit), NULL);
	gtk_menu_shell_append(GTK_MENU_SHELL(menu), quit_item);

	gtk_widget_show_all(menu);
	app_indicator_set_menu(g_indicator, GTK_MENU(menu));
	app_indicator_set_status(g_indicator, APP_INDICATOR_STATUS_ACTIVE);
}

static void tray_set_status(const char *label) {
	if (!g_status_item) return;
	GtkLabel *lbl = GTK_LABEL(gtk_bin_get_child(GTK_BIN(g_status_item)));
	if (lbl) gtk_label_set_text(lbl, label);
}

// Intercept the GTK window X button: hide instead of destroy.
static gboolean on_delete_event(GtkWidget *w, GdkEvent *ev, gpointer d) {
	gtk_widget_hide(w);
	return TRUE; // prevent destruction / gtk_main_quit
}

static void tray_intercept_close(void *window) {
	g_signal_connect(GTK_WIDGET(window), "delete-event",
	                 G_CALLBACK(on_delete_event), NULL);
}
*/
import "C"

import (
	"net/http"
	"time"
	"unsafe"
)

// trayWindow holds the GTK window pointer so tray callbacks can show it.
var trayWindow unsafe.Pointer

//export go_tray_open
func go_tray_open() {
	showWindow(trayWindow)
}

//export go_tray_quit
func go_tray_quit() {
	quit()
}

// initTray creates the AppIndicator and intercepts the window delete-event.
// Must be called from the GTK main thread before wv.Run().
func initTray(windowPtr unsafe.Pointer) {
	trayWindow = windowPtr
	icon := C.CString("ollama-app")
	defer C.free(unsafe.Pointer(icon))
	C.tray_init(icon)
	C.tray_intercept_close(windowPtr)
}

// startTrayHealthCheck polls Ollama every 15 s and updates the tray label.
// Uses gtkDispatch to update the GTK label on the main thread.
func startTrayHealthCheck() {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		check := func() {
			resp, err := http.Get("http://localhost:11434/api/version")
			var label string
			if err != nil || resp.StatusCode != http.StatusOK {
				label = "Ollama: offline"
			} else {
				resp.Body.Close()
				label = "Ollama: running"
			}
			cl := C.CString(label)
			gtkDispatch(func() {
				C.tray_set_status(cl)
				C.free(unsafe.Pointer(cl))
			})
		}

		check()
		for range ticker.C {
			check()
		}
	}()
}
