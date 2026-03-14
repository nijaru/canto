package skill

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load parses a SKILL.md file and returns a Skill.
// Format must be:
// ---
// name: skill-name
// description: skill-description
// ---
// skill-instructions
func Load(path string) (*Skill, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Normalize CRLF to LF
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))

	lines := bytes.Split(b, []byte("\n"))
	var frontmatterLines [][]byte
	var instructionLines [][]byte
	inFrontmatter := false
	frontmatterEnded := false

	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.Equal(trimmed, []byte("---")) {
			if !inFrontmatter && !frontmatterEnded {
				if i == 0 {
					inFrontmatter = true
					continue
				}
			} else if inFrontmatter {
				inFrontmatter = false
				frontmatterEnded = true
				continue
			}
		}

		if inFrontmatter {
			frontmatterLines = append(frontmatterLines, line)
		} else if frontmatterEnded {
			instructionLines = append(instructionLines, line)
		}
	}

	if !frontmatterEnded {
		return nil, fmt.Errorf("invalid SKILL.md: missing or incomplete --- frontmatter markers")
	}

	s := &Skill{}
	if err := yaml.Unmarshal(bytes.Join(frontmatterLines, []byte("\n")), s); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	s.Instructions = string(bytes.TrimSpace(bytes.Join(instructionLines, []byte("\n"))))
	return s, nil
}
