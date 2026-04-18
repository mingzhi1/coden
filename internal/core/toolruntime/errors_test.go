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

func TestClassifyShellError_MultiLanguageCompile(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
	}{
		{"typescript", "src/index.ts:5:10 - error TS2304: Cannot find name 'Foo'."},
		{"rust", "error[E0433]: failed to resolve: use of undeclared crate or module `foo`"},
		{"python", "  File \"main.py\", line 3\nSyntaxError: invalid syntax"},
		{"java", "Main.java:5: error: cannot find symbol"},
		{"cpp_fatal", `main.cpp:2:10: fatal error: 'missing.h' file not found`},
		{"cpp_undef_ref", `build/main.o: undefined reference to 'foo()'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce := toolruntime.ClassifyShellError("build", "", tc.stderr, 1, false)
			if ce == nil {
				t.Fatal("expected classified error")
			}
			if ce.Class != toolruntime.ErrorClassCompileError {
				t.Errorf("expected compile_error, got %q", ce.Class)
			}
		})
	}
}

func TestClassifyShellError_MultiLanguageTestFailure(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
	}{
		{"pytest", "FAILED tests/test_main.py::test_add - AssertionError"},
		{"jest", "Tests: 2 failed, 3 passed\nTest Suites: 1 failed"},
		{"cargo_test", "test result: FAILED. 1 passed; 1 failed; 0 ignored"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce := toolruntime.ClassifyShellError("test", tc.stdout, "", 1, false)
			if ce == nil {
				t.Fatal("expected classified error")
			}
			if ce.Class != toolruntime.ErrorClassTestFailure {
				t.Errorf("expected test_failure, got %q", ce.Class)
			}
		})
	}
}
