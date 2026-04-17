package inventory

import "runtime"

// Platform holds OS-specific settings for tool discovery.
type Platform struct {
	// Suffix is appended to executable names (e.g. ".exe" on Windows).
	Suffix string
	// WhichCommand is the system command to locate executables.
	WhichCommand string
}

// CurrentPlatform returns the Platform for the running OS.
func CurrentPlatform() Platform {
	switch runtime.GOOS {
	case "windows":
		return Platform{Suffix: ".exe", WhichCommand: "where"}
	case "darwin":
		return Platform{Suffix: "", WhichCommand: "which"}
	default: // linux, freebsd, etc.
		return Platform{Suffix: "", WhichCommand: "which"}
	}
}

// ResolveCommand adjusts a command name for the current platform.
// On Windows, it appends ".exe" if not already present.
func (p Platform) ResolveCommand(cmd string) string {
	if p.Suffix == "" {
		return cmd
	}
	if len(cmd) > len(p.Suffix) && cmd[len(cmd)-len(p.Suffix):] == p.Suffix {
		return cmd // already has suffix
	}
	return cmd + p.Suffix
}
