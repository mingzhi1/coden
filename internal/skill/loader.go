package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const skillFileName = "SKILL.md"

// LoadSkillsFromDir scans a directory for skill subdirectories.
// Each subdirectory must contain a SKILL.md file.
func LoadSkillsFromDir(dir string, source Source) ([]*Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // directory doesn't exist — not an error
		}
		return nil, fmt.Errorf("read skills dir %s: %w", dir, err)
	}

	var skills []*Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name(), skillFileName)
		if _, statErr := os.Stat(skillPath); os.IsNotExist(statErr) {
			continue
		}
		s, parseErr := ParseSkillFile(skillPath, source)
		if parseErr != nil {
			// Log warning but don't fail — skip broken skills
			continue
		}
		skills = append(skills, s)
	}
	return skills, nil
}

// LoadRulesFile loads a RULES.md file as an implicit skill.
// The file does not need frontmatter; a default name is generated.
func LoadRulesFile(path string, source Source) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, fmt.Errorf("empty rules file: %s", path)
	}

	// Try to parse frontmatter if present
	fm, body, fmErr := parseFrontmatter(data)
	if fmErr != nil || fm.Name == "" {
		fm = Frontmatter{
			Name:        "project-rules",
			Description: "Project rules from " + filepath.Base(path),
			WhenToUse:   "Always active — follow these rules in all interactions",
		}
		body = content
	}

	return &Skill{
		Frontmatter: fm,
		Content:     body,
		SourcePath:  path,
		LoadedFrom:  source,
		LoadedAt:    time.Now(),
	}, nil
}

// ParseSkillFile parses a SKILL.md file into a Skill.
func ParseSkillFile(path string, source Source) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skill file %s: %w", path, err)
	}

	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parse skill %s: %w", path, err)
	}

	if fm.Name == "" {
		// Derive name from directory
		fm.Name = filepath.Base(filepath.Dir(path))
	}

	return &Skill{
		Frontmatter: fm,
		Content:     body,
		SourcePath:  path,
		LoadedFrom:  source,
		LoadedAt:    time.Now(),
	}, nil
}

// parseFrontmatter extracts YAML between --- delimiters.
// Returns the frontmatter and the remaining markdown body.
func parseFrontmatter(data []byte) (Frontmatter, string, error) {
	content := string(data)
	content = strings.TrimSpace(content)

	if !strings.HasPrefix(content, "---") {
		return Frontmatter{}, content, nil
	}

	// Find the closing ---
	rest := content[3:] // skip opening ---
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Frontmatter{}, content, nil
	}

	yamlStr := strings.TrimSpace(rest[:idx])
	body := strings.TrimSpace(rest[idx+4:]) // skip \n---

	var fm Frontmatter
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlStr)))
	if err := decoder.Decode(&fm); err != nil {
		return Frontmatter{}, content, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}

	return fm, body, nil
}

// UserSkillsDir returns the user-level skills directory.
func UserSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".coden", "skills")
}

// ProjectSkillsDir returns the project-level skills directory.
func ProjectSkillsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".coden", "skills")
}

// ProjectRulesPath returns the path to the project RULES.md file.
func ProjectRulesPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".coden", "RULES.md")
}
