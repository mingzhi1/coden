package toolruntime

import (
	"fmt"
	"regexp"
	"strings"
)

// CommandSemantics classifies the intent of a shell command.
type CommandSemantics string

const (
	CmdSearch    CommandSemantics = "search"
	CmdRead      CommandSemantics = "read"
	CmdBuild     CommandSemantics = "build"
	CmdTest      CommandSemantics = "test"
	CmdWrite     CommandSemantics = "write"
	CmdDelete    CommandSemantics = "delete"
	CmdNetwork   CommandSemantics = "network"
	CmdDangerous CommandSemantics = "dangerous"
	CmdUnknown   CommandSemantics = "unknown"
)

// SecurityViolation describes why a command was blocked.
type SecurityViolation struct {
	Type    string // "command_substitution" | "dangerous_command"
	Detail  string // human-readable description
	Pattern string // matched pattern
}

func (v *SecurityViolation) Error() string {
	return fmt.Sprintf("[%s] %s", v.Type, v.Detail)
}

// --- Command substitution detection ---

var substitutionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\$\(`),        // $(...)
	regexp.MustCompile("`"),           // `...` backtick substitution
	regexp.MustCompile(`\$\{[^}]+}`), // ${VAR} variable expansion
	regexp.MustCompile(`<\(`),         // <(...) process substitution
	regexp.MustCompile(`>\(`),         // >(...) process substitution
}

// CheckCommandSubstitution detects shell substitution patterns that could
// allow arbitrary code execution. Returns nil if the command is safe.
func CheckCommandSubstitution(command string) *SecurityViolation {
	for _, pat := range substitutionPatterns {
		if loc := pat.FindStringIndex(command); loc != nil {
			return &SecurityViolation{
				Type: "command_substitution",
				Detail: fmt.Sprintf(
					"Command contains shell substitution at position %d: %q. Use explicit arguments instead of shell expansion.",
					loc[0], safeSnippet(command, loc[0], 30),
				),
				Pattern: pat.String(),
			}
		}
	}
	return nil
}

// --- Dangerous command detection ---

// dangerousPatterns maps substring patterns to human-readable reasons.
var dangerousPatterns = map[string]string{
	"rm -rf /":                  "recursive delete of root filesystem",
	"rm -rf /*":                 "recursive delete of root filesystem",
	"mkfs":                      "filesystem format tool",
	"dd if=/dev":                "raw disk write",
	":(){:|:&};:":               "fork bomb",
	"chmod -R 777":              "recursive world-writable permissions",
	"> /dev/sda":                "raw disk overwrite",
	"> /dev/nvme":               "raw disk overwrite",
	"format c:":                 "Windows disk format",
	"/.ssh/authorized_keys":     "write to SSH authorized keys",
	"/.ssh/config":              "write to SSH config",
	"/etc/passwd":               "access to /etc/passwd",
	"/etc/shadow":               "access to /etc/shadow",
	"/etc/sudoers":              "access to sudoers file",
}

// exfiltrationPatterns catches pipes to network tools that may exfiltrate data.
// These are regexes matched against the full command.
var exfiltrationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\|\s*(curl|wget|nc|ncat|netcat|socat)\s`),
	regexp.MustCompile(`\|\s*(curl|wget|nc|ncat|netcat|socat)$`),
}

// CheckExfiltration detects commands that pipe output to network tools.
func CheckExfiltration(command string) *SecurityViolation {
	lower := strings.ToLower(command)
	for _, pat := range exfiltrationPatterns {
		if loc := pat.FindStringIndex(lower); loc != nil {
			return &SecurityViolation{
				Type: "dangerous_command",
				Detail: fmt.Sprintf(
					"Command pipes output to a network tool at position %d: %q. Potential data exfiltration.",
					loc[0], safeSnippet(command, loc[0], 40),
				),
				Pattern: pat.String(),
			}
		}
	}
	return nil
}

// dangerousBaseCommands maps the first token to a reason.
var dangerousBaseCommands = map[string]string{
	"mkfs":     "filesystem format tool",
	"fdisk":    "disk partition tool",
	"parted":   "disk partition tool",
	"shutdown": "system shutdown",
	"reboot":   "system reboot",
	"init":     "init system control",
	"systemctl": "system service control",
}

// CheckDangerousCommand checks if a command matches known dangerous patterns.
// Returns nil if the command is safe.
func CheckDangerousCommand(command string) *SecurityViolation {
	lower := strings.ToLower(strings.TrimSpace(command))

	// Check substring patterns.
	for pattern, reason := range dangerousPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return &SecurityViolation{
				Type:    "dangerous_command",
				Detail:  fmt.Sprintf("Command blocked: %s. This operation is not permitted.", reason),
				Pattern: pattern,
			}
		}
	}

	// Check base command (first token).
	base := extractBaseCommand(lower)
	if reason, ok := dangerousBaseCommands[base]; ok {
		return &SecurityViolation{
			Type:    "dangerous_command",
			Detail:  fmt.Sprintf("Command %q blocked: %s. This operation is not permitted.", base, reason),
			Pattern: base,
		}
	}

	return nil
}

// --- Command semantic classification ---

func newSet(items ...string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

var (
	searchCommands  = newSet("find", "grep", "rg", "ag", "fd", "fzf", "locate", "where", "which")
	readCommands    = newSet("cat", "head", "tail", "less", "more", "jq", "yq", "wc", "file", "stat", "type", "echo", "printf")
	writeCommands   = newSet("mv", "cp", "mkdir", "touch", "tee", "ln", "install")
	deleteCommands  = newSet("rm", "rmdir", "del")
	networkCommands = newSet("curl", "wget", "ssh", "scp", "rsync", "nc", "nmap")
)

// buildSubCmds maps multi-word commands like "go build" to semantics.
var buildSubCmds = newSet("build", "install", "run", "generate", "mod")
var testSubCmds  = newSet("test", "bench", "vet")

// ClassifyCommand returns the semantic category of a shell command.
func ClassifyCommand(command string) CommandSemantics {
	command = strings.TrimSpace(command)
	if command == "" {
		return CmdUnknown
	}

	base := extractBaseCommand(strings.ToLower(command))
	sub := extractSubCommand(strings.ToLower(command))

	// Check dangerous first.
	if _, ok := dangerousBaseCommands[base]; ok {
		return CmdDangerous
	}

	// Multi-word commands: go, npm, yarn, cargo, pip, etc.
	if isCompoundBuildTool(base) {
		if testSubCmds[sub] {
			return CmdTest
		}
		if buildSubCmds[sub] {
			return CmdBuild
		}
		// go fmt, npm start, etc. → build category
		return CmdBuild
	}

	// Dedicated test runners.
	if base == "pytest" || base == "jest" || base == "vitest" {
		return CmdTest
	}

	// Single-token classification.
	if searchCommands[base] {
		return CmdSearch
	}
	if readCommands[base] {
		return CmdRead
	}
	if deleteCommands[base] {
		return CmdDelete
	}
	if writeCommands[base] {
		return CmdWrite
	}
	if networkCommands[base] {
		return CmdNetwork
	}
	if base == "git" {
		return classifyGitCommand(sub)
	}
	if base == "make" || base == "cmake" {
		return CmdBuild
	}

	return CmdUnknown
}

// --- helpers ---

// extractBaseCommand returns the first whitespace-delimited token, lowercased.
func extractBaseCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// extractSubCommand returns the second token if present.
func extractSubCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// isCompoundBuildTool returns true for tools that need sub-command inspection.
func isCompoundBuildTool(base string) bool {
	switch base {
	case "go", "npm", "yarn", "pnpm", "cargo", "pip", "uv",
		"poetry", "dotnet", "mvn", "gradle", "bun":
		return true
	}
	return false
}

// classifyGitCommand maps git sub-commands to semantics.
func classifyGitCommand(sub string) CommandSemantics {
	switch sub {
	case "log", "show", "diff", "status", "branch", "tag", "blame", "shortlog":
		return CmdRead
	case "add", "commit", "merge", "rebase", "checkout", "switch", "stash",
		"reset", "cherry-pick", "push", "pull", "fetch":
		return CmdWrite
	case "grep":
		return CmdSearch
	case "rm", "clean":
		return CmdDelete
	case "clone":
		return CmdNetwork
	}
	return CmdRead // default for git
}

// safeSnippet extracts a short snippet around position pos for error messages.
func safeSnippet(s string, pos, maxLen int) string {
	start := pos
	if start < 0 {
		start = 0
	}
	end := start + maxLen
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
