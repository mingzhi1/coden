package toolruntime

import (
	"testing"
)

// --- CheckCommandSubstitution tests ---

func TestCheckCommandSubstitution_DollarParen(t *testing.T) {
	v := CheckCommandSubstitution("echo $(whoami)")
	if v == nil {
		t.Fatal("expected violation for $(whoami)")
	}
	if v.Type != "command_substitution" {
		t.Fatalf("expected type command_substitution, got %s", v.Type)
	}
}

func TestCheckCommandSubstitution_Backtick(t *testing.T) {
	v := CheckCommandSubstitution("echo `id`")
	if v == nil {
		t.Fatal("expected violation for backtick")
	}
	if v.Type != "command_substitution" {
		t.Fatalf("expected type command_substitution, got %s", v.Type)
	}
}

func TestCheckCommandSubstitution_BraceExpansion(t *testing.T) {
	v := CheckCommandSubstitution("echo ${HOME}")
	if v == nil {
		t.Fatal("expected violation for ${HOME}")
	}
}

func TestCheckCommandSubstitution_ProcessSubstitution(t *testing.T) {
	v := CheckCommandSubstitution("diff <(ls dir1) <(ls dir2)")
	if v == nil {
		t.Fatal("expected violation for <()")
	}
}

func TestCheckCommandSubstitution_Safe(t *testing.T) {
	safe := []string{
		"go build ./...",
		"rg pattern .",
		"cat README.md",
		"npm test",
		"git status",
		"echo hello world",
	}
	for _, cmd := range safe {
		if v := CheckCommandSubstitution(cmd); v != nil {
			t.Errorf("expected nil for safe command %q, got %v", cmd, v)
		}
	}
}

// --- CheckDangerousCommand tests ---

func TestCheckDangerousCommand_RmRfRoot(t *testing.T) {
	v := CheckDangerousCommand("rm -rf /")
	if v == nil {
		t.Fatal("expected violation for rm -rf /")
	}
	if v.Type != "dangerous_command" {
		t.Fatalf("expected type dangerous_command, got %s", v.Type)
	}
}

func TestCheckDangerousCommand_BaseCommand(t *testing.T) {
	cases := []string{"mkfs /dev/sda1", "fdisk /dev/sda", "shutdown -h now", "reboot"}
	for _, cmd := range cases {
		v := CheckDangerousCommand(cmd)
		if v == nil {
			t.Errorf("expected violation for %q", cmd)
		}
	}
}

func TestCheckDangerousCommand_Safe(t *testing.T) {
	safe := []string{
		"go build ./...",
		"rm -rf ./build",
		"npm install",
		"git push origin main",
	}
	for _, cmd := range safe {
		if v := CheckDangerousCommand(cmd); v != nil {
			t.Errorf("expected nil for safe command %q, got %v", cmd, v)
		}
	}
}

// --- ClassifyCommand tests ---

func TestClassifyCommand_GoBuild(t *testing.T) {
	if got := ClassifyCommand("go build ./..."); got != CmdBuild {
		t.Fatalf("expected build, got %s", got)
	}
}

func TestClassifyCommand_GoTest(t *testing.T) {
	if got := ClassifyCommand("go test ./..."); got != CmdTest {
		t.Fatalf("expected test, got %s", got)
	}
}

func TestClassifyCommand_NpmTest(t *testing.T) {
	if got := ClassifyCommand("npm test"); got != CmdTest {
		t.Fatalf("expected test, got %s", got)
	}
}

func TestClassifyCommand_Search(t *testing.T) {
	cases := map[string]CommandSemantics{
		"rg pattern .":  CmdSearch,
		"grep -r foo .": CmdSearch,
		"find . -name":  CmdSearch,
	}
	for cmd, want := range cases {
		if got := ClassifyCommand(cmd); got != want {
			t.Errorf("ClassifyCommand(%q) = %s, want %s", cmd, got, want)
		}
	}
}

func TestClassifyCommand_Read(t *testing.T) {
	cases := map[string]CommandSemantics{
		"cat README.md":  CmdRead,
		"head -20 go.mod": CmdRead,
		"wc -l file.go":  CmdRead,
	}
	for cmd, want := range cases {
		if got := ClassifyCommand(cmd); got != want {
			t.Errorf("ClassifyCommand(%q) = %s, want %s", cmd, got, want)
		}
	}
}

func TestClassifyCommand_GitSubcommands(t *testing.T) {
	cases := map[string]CommandSemantics{
		"git status":    CmdRead,
		"git log":       CmdRead,
		"git diff":      CmdRead,
		"git add .":     CmdWrite,
		"git commit -m": CmdWrite,
		"git grep foo":  CmdSearch,
		"git rm file":   CmdDelete,
		"git clone url": CmdNetwork,
	}
	for cmd, want := range cases {
		if got := ClassifyCommand(cmd); got != want {
			t.Errorf("ClassifyCommand(%q) = %s, want %s", cmd, got, want)
		}
	}
}

func TestClassifyCommand_Dangerous(t *testing.T) {
	if got := ClassifyCommand("mkfs /dev/sda1"); got != CmdDangerous {
		t.Fatalf("expected dangerous, got %s", got)
	}
}

func TestClassifyCommand_Network(t *testing.T) {
	if got := ClassifyCommand("curl https://example.com"); got != CmdNetwork {
		t.Fatalf("expected network, got %s", got)
	}
}

func TestClassifyCommand_Delete(t *testing.T) {
	if got := ClassifyCommand("rm -rf ./build"); got != CmdDelete {
		t.Fatalf("expected delete, got %s", got)
	}
}

func TestClassifyCommand_Empty(t *testing.T) {
	if got := ClassifyCommand(""); got != CmdUnknown {
		t.Fatalf("expected unknown, got %s", got)
	}
}
