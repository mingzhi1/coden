package llm

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// profilingEnabled is read once at init time from the environment.
// When false, all Profiler methods are zero-cost no-ops.
var profilingEnabled bool

func init() {
	profilingEnabled = os.Getenv("CODEN_PROFILE_QUERY") == "1"
}

// mark is a single checkpoint recorded by the Profiler.
type mark struct {
	Name     string
	Elapsed  time.Duration
	MemAlloc uint64
}

// Profiler records checkpoint timestamps through the query pipeline.
// Enabled via CODEN_PROFILE_QUERY=1 environment variable.
// When disabled, all methods are zero-cost no-ops.
type Profiler struct {
	mu    sync.Mutex
	marks []mark
	start time.Time
	count int
}

// NewProfiler creates a new Profiler. If profiling is disabled, the returned
// profiler's methods are all no-ops with zero allocation overhead.
func NewProfiler() *Profiler {
	if !profilingEnabled {
		return &Profiler{}
	}
	return &Profiler{
		start: time.Now(),
		marks: make([]mark, 0, 16),
	}
}

// Checkpoint records the current elapsed time and memory allocation under the
// given name. When profiling is disabled this is a no-op.
func (p *Profiler) Checkpoint(name string) {
	if !profilingEnabled {
		return
	}

	elapsed := time.Since(p.start)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	p.mu.Lock()
	p.marks = append(p.marks, mark{
		Name:     name,
		Elapsed:  elapsed,
		MemAlloc: mem.Alloc,
	})
	p.mu.Unlock()
}

// Report generates a formatted profiling report and returns it as a string.
// When profiling is disabled or no checkpoints have been recorded, an empty
// string is returned.
func (p *Profiler) Report() string {
	if !profilingEnabled {
		return ""
	}

	p.mu.Lock()
	// Snapshot under lock so we can release quickly.
	marks := make([]mark, len(p.marks))
	copy(marks, p.marks)
	p.count++
	reportNum := p.count
	p.mu.Unlock()

	if len(marks) == 0 {
		return ""
	}

	const separator = "================================================================================"

	var sb strings.Builder
	sb.Grow(512)

	sb.WriteString(separator)
	sb.WriteByte('\n')
	sb.WriteString(fmt.Sprintf("QUERY PROFILING REPORT #%d", reportNum))
	sb.WriteByte('\n')
	sb.WriteString(separator)
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	var prevElapsed time.Duration

	for _, m := range marks {
		delta := m.Elapsed - prevElapsed
		prevElapsed = m.Elapsed

		elapsedMs := m.Elapsed.Milliseconds()
		deltaMs := delta.Milliseconds()
		memMB := m.MemAlloc / (1024 * 1024)

		// Build the warning suffix.
		var warning string
		if delta > time.Second {
			warning = "  ⚠️ VERY SLOW"
		} else if delta > 100*time.Millisecond {
			warning = "  ⚠️ SLOW"
		}

		sb.WriteString(fmt.Sprintf("%8dms  %8sms  %6dMB  %s%s\n",
			elapsedMs,
			fmt.Sprintf("Δ%d", deltaMs),
			memMB,
			m.Name,
			warning,
		))
	}

	// TTFT breakdown: if both "api_request_sent" and "first_chunk_received"
	// checkpoints exist, show a time-to-first-token summary line.
	var apiSentElapsed, firstChunkElapsed time.Duration
	var foundSent, foundChunk bool

	for _, m := range marks {
		switch m.Name {
		case "api_request_sent":
			apiSentElapsed = m.Elapsed
			foundSent = true
		case "first_chunk_received":
			firstChunkElapsed = m.Elapsed
			foundChunk = true
		}
	}

	if foundSent && foundChunk {
		ttft := firstChunkElapsed.Milliseconds()
		preReq := apiSentElapsed.Milliseconds()
		network := (firstChunkElapsed - apiSentElapsed).Milliseconds()

		sb.WriteByte('\n')
		sb.WriteString(fmt.Sprintf("TTFT: %dms (pre-request: %dms, network: %dms)\n",
			ttft, preReq, network,
		))
	}

	sb.WriteString(separator)
	sb.WriteByte('\n')

	return sb.String()
}

// Reset clears all recorded checkpoints and resets the start time.
// When profiling is disabled this is a no-op.
func (p *Profiler) Reset() {
	if !profilingEnabled {
		return
	}

	p.mu.Lock()
	p.marks = p.marks[:0]
	p.start = time.Now()
	p.mu.Unlock()
}
