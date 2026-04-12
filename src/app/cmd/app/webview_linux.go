//go:build linux

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ollama/ollama/app/store"
	"github.com/ollama/ollama/app/webview"
)

type Webview struct {
	port    int
	token   string
	webview webview.WebView
	mutex   sync.Mutex

	Store *store.Store
}

// gtkDispatch schedules f on the GTK main thread.
// Set once the inner webview is created; safe to call from any goroutine.
var gtkDispatch func(func())

// Run initialises the webview window and navigates to path.
// Must be called from the main OS thread (ensured by webview.init()).
func (w *Webview) Run(path string) unsafe.Pointer {
	var url string
	if devMode {
		url = fmt.Sprintf("http://localhost:5173%s", path)
	} else {
		url = fmt.Sprintf("http://127.0.0.1:%d%s", w.port, path)
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.webview == nil {
		wv := webview.New(debug)

		// Start hidden; the JS "ready" binding shows the window.
		hideWindow(wv.Window())
		wv.SetTitle("Ollama")

		init := `
		// Disable Ctrl+R reload
		document.addEventListener('keydown', function(e) {
			if ((e.ctrlKey || e.metaKey) && e.key === 'r') {
				e.preventDefault();
				return false;
			}
		});

		// Prevent back/forward navigation
		window.addEventListener('popstate', function(e) {
			e.preventDefault();
			history.pushState(null, '', window.location.pathname);
			return false;
		});

		window.addEventListener('load', function() {
			history.pushState(null, '', window.location.pathname);
			window.history.replaceState(null, '', window.location.pathname);
		});

		// Set auth token cookie
		document.cookie = "token=` + w.token + `; path=/";

		// Ctrl+N → new chat
		document.addEventListener('keydown', function(e) {
			if ((e.ctrlKey || e.metaKey) && e.key === 'n') {
				e.preventDefault();
				history.pushState({}, '', '/c/new');
				window.dispatchEvent(new PopStateEvent('popstate'));
				return false;
			}
		});

		window.OLLAMA_WEBSEARCH = true;
		`
		wv.Init(init)

		// Zoom keyboard shortcuts
		wv.Init(`
			window.addEventListener('keydown', function(e) {
				if ((e.metaKey || e.ctrlKey) && (e.key === '+' || e.key === '=')) {
					e.preventDefault();
					window.zoomIn && window.zoomIn();
				}
				if ((e.metaKey || e.ctrlKey) && e.key === '-') {
					e.preventDefault();
					window.zoomOut && window.zoomOut();
				}
				if ((e.metaKey || e.ctrlKey) && e.key === '0') {
					e.preventDefault();
					window.zoomReset && window.zoomReset();
				}
			}, true);
		`)

		wv.Bind("zoomIn", func() {
			wv.SetZoom(wv.GetZoom() + 0.1)
		})
		wv.Bind("zoomOut", func() {
			wv.SetZoom(wv.GetZoom() - 0.1)
		})
		wv.Bind("zoomReset", func() {
			wv.SetZoom(1.0)
		})

		wv.Bind("ready", func() {
			showWindow(wv.Window())
		})

		wv.Bind("close", func() {
			// Hide the window — the tray keeps the app alive.
			// Use the tray "Quit" menu item to exit entirely.
			hideWindow(wv.Window())
		})

		// File system access (file attachment in chat)
		wv.Bind("selectModelsDirectory", func() {
			go func() {
				callCallback := func(data interface{}) {
					dataJSON, _ := json.Marshal(data)
					wv.Dispatch(func() {
						wv.Eval(fmt.Sprintf("window.__selectModelsDirectoryCallback && window.__selectModelsDirectoryCallback(%s)", dataJSON))
					})
				}
				directory, err := selectDirectory("Select Model Directory")
				if err != nil {
					callCallback(nil)
					return
				}
				callCallback(directory)
			}()
		})

		wv.Bind("selectFiles", func() {
			go func() {
				callCallback := func(data interface{}) {
					dataJSON, _ := json.Marshal(data)
					wv.Dispatch(func() {
						wv.Eval(fmt.Sprintf("window.__selectFilesCallback && window.__selectFilesCallback(%s)", dataJSON))
					})
				}

				textExts := []string{
					"pdf", "docx", "txt", "md", "csv", "json", "xml", "html", "htm",
					"js", "jsx", "ts", "tsx", "py", "java", "cpp", "c", "cc", "h", "cs", "php", "rb",
					"go", "rs", "swift", "kt", "scala", "sh", "bat", "yaml", "yml", "toml", "ini",
					"cfg", "conf", "log", "rtf",
				}
				imageExts := []string{"png", "jpg", "jpeg", "webp"}
				allowedExts := append(textExts, imageExts...)

				filenames, err := selectMultipleFiles("Select Files", allowedExts)
				if err != nil || len(filenames) == 0 {
					callCallback(nil)
					return
				}

				var files []map[string]string
				maxFileSize := int64(10 * 1024 * 1024)

				for _, filename := range filenames {
					ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
					validExt := false
					for _, allowed := range allowedExts {
						if ext == allowed {
							validExt = true
							break
						}
					}
					if !validExt {
						continue
					}
					info, err := os.Stat(filename)
					if err != nil || info.Size() > maxFileSize {
						continue
					}
					fileBytes, err := os.ReadFile(filename)
					if err != nil {
						continue
					}
					mimeType := http.DetectContentType(fileBytes)
					dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(fileBytes))
					files = append(files, map[string]string{
						"filename": filepath.Base(filename),
						"path":     filename,
						"dataURL":  dataURL,
					})
				}

				if len(files) == 0 {
					callCallback(nil)
				} else {
					callCallback(files)
				}
			}()
		})

		wv.Bind("selectWorkingDirectory", func() {
			go func() {
				callCallback := func(data interface{}) {
					dataJSON, _ := json.Marshal(data)
					wv.Dispatch(func() {
						wv.Eval(fmt.Sprintf("window.__selectWorkingDirectoryCallback && window.__selectWorkingDirectoryCallback(%s)", dataJSON))
					})
				}
				directory, err := selectDirectory("Select Working Directory")
				if err != nil {
					callCallback(nil)
					return
				}
				callCallback(directory)
			}()
		})

		// Context menu items — no-op on Linux (no native right-click menu)
		wv.Bind("setContextMenuItems", func(_ []map[string]interface{}) error {
			return nil
		})

		// Drag to move the window
		wv.Bind("drag", func() {
			wv.Dispatch(func() {
				drag(wv.Window())
			})
		})

		// Double-click title bar toggles maximize
		wv.Bind("doubleClick", func() {
			wv.Dispatch(func() {
				doubleClick(wv.Window())
			})
		})

		// Persist window size
		var resizeTimer *time.Timer
		var resizeMutex sync.Mutex
		wv.Bind("resize", func(width, height int) {
			if w.Store != nil {
				resizeMutex.Lock()
				if resizeTimer != nil {
					resizeTimer.Stop()
				}
				resizeTimer = time.AfterFunc(100*time.Millisecond, func() {
					if err := w.Store.SetWindowSize(width, height); err != nil {
						slog.Error("failed to set window size", "error", err)
					}
				})
				resizeMutex.Unlock()
			}
		})

		// Restore persisted size, then set min size
		if w.Store != nil {
			width, height, err := w.Store.WindowSize()
			if err != nil {
				slog.Error("failed to get window size", "error", err)
			}
			if width > 0 && height > 0 {
				wv.SetSize(width, height, webview.HintNone)
			} else {
				wv.SetSize(1024, 768, webview.HintNone)
			}
		}
		wv.SetSize(800, 600, webview.HintMin)

		w.webview = wv
		gtkDispatch = wv.Dispatch    // expose dispatch for tray health-check
		initTray(wv.Window())        // create tray + intercept GTK X button
		startTrayHealthCheck()       // background health poller
		wv.Navigate(url)
		wv.Run() // blocks until quit() calls wv.Terminate()
	} else {
		w.webview.Eval(fmt.Sprintf(`history.pushState({}, '', '%s');`, path))
		showWindow(w.webview.Window())
	}

	return nil
}

func (w *Webview) Terminate() {
	w.mutex.Lock()
	wv := w.webview
	w.webview = nil
	w.mutex.Unlock()
	if wv != nil {
		wv.Terminate()
		wv.Destroy()
	}
}

func (w *Webview) IsRunning() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.webview != nil
}
