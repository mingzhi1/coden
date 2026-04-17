package skill

import (
	"path/filepath"
	"strings"
)

// matchGlob matches a gitignore-style pattern against a file path.
// Supports:
//   - "*" matches any non-separator characters
//   - "**" matches any path segments
//   - "?" matches any single character
func matchGlob(pattern, path string) bool {
	// Normalize separators
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	// Handle ** pattern
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, path)
	}

	// Use filepath.Match for simple patterns
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}

	// Also try matching against just the filename
	matched, _ = filepath.Match(pattern, filepath.Base(path))
	return matched
}

// matchDoublestar handles ** glob patterns.
func matchDoublestar(pattern, path string) bool {
	parts := strings.Split(pattern, "**")
	if len(parts) != 2 {
		// Multiple ** not supported, fallback
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")

	if prefix != "" && !strings.HasPrefix(path, prefix+"/") && path != prefix {
		return false
	}

	if suffix == "" {
		return true
	}

	// Check if any suffix of the path matches the suffix pattern
	segments := strings.Split(path, "/")
	for i := range segments {
		subpath := strings.Join(segments[i:], "/")
		matched, _ := filepath.Match(suffix, subpath)
		if matched {
			return true
		}
		// Also match just the filename part
		if i == len(segments)-1 {
			matched, _ = filepath.Match(suffix, segments[i])
			if matched {
				return true
			}
		}
	}
	return false
}
