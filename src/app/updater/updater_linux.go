//go:build linux

package updater

import "runtime"

func init() {
	UserAgentOS = "Linux/" + runtime.GOARCH
	// UpgradeMarkerFile stays empty — systemd manages the daemon on Linux,
	// so there is never a pending self-update.
	UpgradeMarkerFile = ""
	VerifyDownload = verifyDownload
}

func DoUpgrade(_ bool) error      { return nil }
func DoPostUpgradeCleanup() error { return nil }
func verifyDownload() error       { return nil }
func IsUpdatePending() bool       { return false }
func DoUpgradeAtStartup() error   { return nil }
func getStagedUpdate() string     { return "" }
