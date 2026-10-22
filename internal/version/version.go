package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	// Version is populated at build time via -ldflags "-X github.com/whilstsomebody/securegate/internal/version.Version=...".
	Version = "v0.2.0-dev"
	// Commit is populated at build time via -ldflags.
	Commit = "unknown"
	// BuildDate is populated at build time via -ldflags.
	BuildDate = "unknown"
)

func init() {
	if Commit == "unknown" || Version == "v0.2.0-dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					if Commit == "unknown" && len(setting.Value) >= 7 {
						Commit = setting.Value[:7]
					}
				case "vcs.time":
					if BuildDate == "unknown" {
						BuildDate = setting.Value
					}
				}
			}
		}
	}
}

// Info returns a formatted version string.
func Info() string {
	return fmt.Sprintf("SecureGate %s (commit: %s, built: %s, go: %s)", Version, Commit, BuildDate, runtime.Version())
}
