package toolruntime_test

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

func TestClassifyShellError_Timeout(t *testing.T) {
	ce := toolruntime.ClassifyShellError("go test ./...", "", "signal: killed", -1, true)
	if ce == nil {
		t.Fatal("expected classified error for timeout")
	}
	if ce.Class != toolruntime.ErrorClassTimeout {
		t.Errorf("expected timeout class, got %q", ce.Class)
	}
	if ce.HumanMsg == "" {
		t.Error("expected non-empty human message")
	}
}

func TestClassifyShellError_EnvMissing(t *testing.T) {
	stderr := `exec: "go": executable file not found in PATH`
	ce := toolruntime.ClassifyShellError("go build", "", stderr, 1, false)
	if ce == nil {
		t.Fatal("expected classified error")
	}
	if ce.Class != toolruntime.ErrorClassEnvMissing {
		t.Errorf("expected env_missing, got %q", ce.Class)
	}
}

func TestClassifyShellError_CompileError(t *testing.T) {
	stderr := `./main.go:10:5: undefined: Foo`
	ce := toolruntime.ClassifyShellError("go build .", "", stderr, 1, false)
	if ce == nil {
		t.Fatal("expected classified error")
	}
	if ce.Class != toolruntime.ErrorClassCompileError {
		t.Errorf("expected compile_error, got %q", ce.Class)
	}
}

func TestClassifyShellError_TestFailure(t *testing.T) {
	stdout := `--- FAIL: TestFoo (0.00s)\nFAIL\tcoden/internal/core/foo`
	ce := toolruntime.ClassifyShellError("go test ./...", stdout, "", 1, false)
	if ce == nil {
		t.Fatal("expected classified error")
	}
	if ce.Class != toolruntime.ErrorClassTestFailure {
		t.Errorf("expected test_failure, got %q", ce.Class)
	}
}

func TestClassifyShellError_Permission(t *testing.T) {
	ce := toolruntime.ClassifyShellError("rm -rf /", "", "permission denied", 1, false)
	if ce == nil {
		t.Fatal("expected classified error")
	}
	if ce.Class != toolruntime.ErrorClassPermission {
		t.Errorf("expected permission, got %q", ce.Class)
	}
}

func TestClassifyShellError_Success(t *testing.T) {
	ce := toolruntime.ClassifyShellError("echo hi", "hi", "", 0, false)
	if ce != nil {
		t.Fatalf("expected nil for successful command, got %+v", ce)
	}
}

func TestClassifyShellError_RuntimeFallback(t *testing.T) {
	ce := toolruntime.ClassifyShellError("myapp --run", "", "something went wrong", 2, false)
	if ce == nil {
		t.Fatal("expected classified error")
	}
	if ce.Class != toolruntime.ErrorClassRuntimeError {
		t.Errorf("expected runtime_error, got %q", ce.Class)
	}
	if ce.ExitCode != 2 {
		t.Errorf("expected exit code 2, got %d", ce.ExitCode)
	}
}
