package log

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	initOnce    sync.Once
	initialized atomic.Bool

	// mainLogDir stores the directory of the main log file for deriving
	// session log paths and panic dump paths.
	mainLogDir string
	debugMode  bool

	sessionMu      sync.RWMutex
	sessionLoggers map[string]*slog.Logger
	sessionFiles   map[string]*lumberjack.Logger
)

// Setup initialises the main (global) structured logger backed by a rotating
// JSON file. All slog.Default() calls go here after Setup returns.
//
// logFile    — absolute path, e.g. ~/.coden/coden-main.log
// debug      — when true, LevelDebug is enabled and source location is added
func Setup(logFile string, debug bool) {
	initOnce.Do(func() {
		debugMode = debug
		mainLogDir = filepath.Dir(logFile)
		_ = os.MkdirAll(mainLogDir, 0o755)

		rotator := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    10, // MB per file
			MaxBackups: 3,  // keep 3 rotated files
			MaxAge:     30, // days
			Compress:   false,
		}
		level := slog.LevelInfo
		if debug {
			level = slog.LevelDebug
		}
		handler := slog.NewJSONHandler(rotator, &slog.HandlerOptions{
			Level:     level,
			AddSource: debug,
		})
		slog.SetDefault(slog.New(handler))
		initialized.Store(true)

		sessionLoggers = make(map[string]*slog.Logger)
		sessionFiles = make(map[string]*lumberjack.Logger)
	})
}

// Initialized reports whether Setup has been called.
func Initialized() bool { return initialized.Load() }

// OpenSession opens a per-session JSON log file at
// <mainLogDir>/sessions/<sessionID>.log. Returns a no-op closer if Setup has
// not been called yet or the session is already open.
func OpenSession(sessionID string) func() {
	if sessionID == "" || !initialized.Load() {
		return func() {}
	}

	sessionMu.Lock()
	defer sessionMu.Unlock()

	if _, exists := sessionLoggers[sessionID]; exists {
		// Already open — return a closer anyway so callers can defer it.
		return func() { CloseSession(sessionID) }
	}

	sessDir := filepath.Join(mainLogDir, "sessions")
	_ = os.MkdirAll(sessDir, 0o755)

	rotator := &lumberjack.Logger{
		Filename:   filepath.Join(sessDir, sessionID+".log"),
		MaxSize:    5, // MB per session file
		MaxBackups: 2,
		MaxAge:     14, // days
		Compress:   false,
	}
	level := slog.LevelInfo
	if debugMode {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(rotator, &slog.HandlerOptions{Level: level})
	sessionLoggers[sessionID] = slog.New(handler).With("session_id", sessionID)
	sessionFiles[sessionID] = rotator

	return func() { CloseSession(sessionID) }
}

// Session returns the *slog.Logger for the given session. Falls back to
// slog.Default() when the session has no dedicated logger (or Setup was not
// called).
func Session(sessionID string) *slog.Logger {
	if sessionID == "" {
		return slog.Default()
	}
	sessionMu.RLock()
	l, ok := sessionLoggers[sessionID]
	sessionMu.RUnlock()
	if ok {
		return l
	}
	return slog.Default()
}

// CloseSession flushes and closes the per-session log file, then removes the
// entry from the registry. Safe to call multiple times.
func CloseSession(sessionID string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if f, ok := sessionFiles[sessionID]; ok {
		_ = f.Close()
		delete(sessionFiles, sessionID)
	}
	delete(sessionLoggers, sessionID)
}

// CloseAll closes every open session logger. Call this in kernel.Close().
func CloseAll() {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	for id, f := range sessionFiles {
		_ = f.Close()
		delete(sessionFiles, id)
		delete(sessionLoggers, id)
	}
}

// RecoverPanic catches panics, logs them via slog, and writes a timestamped
// panic dump file to mainLogDir (falls back to the current directory).
func RecoverPanic(name string, cleanup func()) {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("panic recovered", "component", name, "error", r)

	dir := mainLogDir
	if dir == "" {
		dir = "."
	}
	ts := time.Now().Format("20060102-150405")
	dumpPath := filepath.Join(dir, fmt.Sprintf("coden-panic-%s-%s.log", name, ts))
	if f, err := os.Create(dumpPath); err == nil {
		defer f.Close()
		fmt.Fprintf(f, "Panic in %s: %v\n\nTime: %s\n\nStack:\n%s\n",
			name, r, time.Now().Format(time.RFC3339), debug.Stack())
	}
	if cleanup != nil {
		cleanup()
	}
}
