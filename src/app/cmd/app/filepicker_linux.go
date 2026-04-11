//go:build linux

package main

import (
	"os/exec"
	"strings"
)

// selectDirectory opens a GTK directory chooser via zenity.
func selectDirectory(title string) (string, error) {
	args := []string{"--file-selection", "--directory", "--title=" + title}
	out, err := exec.Command("zenity", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// selectMultipleFiles opens a GTK multi-file chooser via zenity.
func selectMultipleFiles(title string, allowedExts []string) ([]string, error) {
	args := []string{
		"--file-selection",
		"--multiple",
		"--separator=\n",
		"--title=" + title,
	}
	// Build a single "all supported" filter
	if len(allowedExts) > 0 {
		var patterns []string
		for _, ext := range allowedExts {
			patterns = append(patterns, "*."+ext)
		}
		args = append(args, "--file-filter=Supported Files | "+strings.Join(patterns, " "))
	}

	out, err := exec.Command("zenity", args...).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}
