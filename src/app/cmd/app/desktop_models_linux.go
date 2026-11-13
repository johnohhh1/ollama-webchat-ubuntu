package main

import "net/http"

// Claude Desktop / Codex model integration is macOS-only upstream.
func desktopModelSettingsHandler() http.Handler { return nil }
