// SPDX-License-Identifier: Apache-2.0

// Package snippet turns an authored snippet into what a reader sees. The UI, the
// API and the CLI all render snippets, so this is the one place that reads the
// file, expands its templates and trims it for reading.
package snippet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// Resolve returns sn as a reader sees it: label, description and apply command
// expanded by resolve, and the body loaded from Path (confined to dir) with long
// comment blocks removed. On a read error the returned snippet still carries
// its resolved text fields, so a caller can report the failure next to them.
func Resolve(dir string, sn scenario.Snippet, resolve func(string) string) (scenario.Snippet, error) {
	sn.Label = resolve(sn.Label)
	sn.Description = resolve(sn.Description)
	sn.Apply = resolve(sn.Apply)

	raw, err := source(dir, sn)
	if err != nil {
		sn.YAML = ""
		return sn, err
	}
	sn.YAML = resolve(trimComments(raw, isShell(sn.Path)))
	sn.ApplyCommand = applyCommand(sn)
	return sn, nil
}

func source(dir string, sn scenario.Snippet) (string, error) {
	if sn.Path == "" {
		return sn.YAML, nil
	}
	return ReadFile(dir, sn.Path)
}

// ReadFile reads rel from inside dir, refusing a path that escapes it: content
// names its files relative to its own directory, and a reader of that content
// must not be able to reach anything else.
func ReadFile(dir, rel string) (string, error) {
	base := filepath.Clean(dir)
	full := filepath.Join(base, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes its directory", rel)
	}
	data, err := os.ReadFile(full) //nolint:gosec // confined to the content directory above
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func applyCommand(sn scenario.Snippet) string {
	switch {
	case !sn.Exercise:
		return ""
	case sn.Apply != "":
		return sn.Apply
	case isShell(sn.Path) || strings.TrimSpace(sn.YAML) == "":
		return ""
	}
	body := strings.TrimRight(sn.YAML, "\n")
	delim := "EOF"
	for slices.Contains(strings.Split(body, "\n"), delim) {
		delim = "SNIPPET_" + delim
	}
	return fmt.Sprintf("kubectl apply -f - <<'%s'\n%s\n%s", delim, body, delim)
}

func isShell(path string) bool { return strings.HasSuffix(path, ".sh") }

var (
	// The character before "<<" is checked so a here-string (<<<) is not taken
	// for a heredoc.
	heredocStart = regexp.MustCompile(`(?:^|[^<])<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)
	blockScalar  = regexp.MustCompile(`^\s*(?:-\s+)?(?:[^#\s][^#]*:\s+)?[|>][-+1-9]{0,2}\s*(?:#.*)?$`)
)

// trimComments removes the file's leading comment banner and every block of two
// or more consecutive comment lines. Single-line comments and comments trailing
// code stay, and heredoc bodies and YAML block scalars are never touched: their
// "#" lines are content, and the trimmed text must still apply.
func trimComments(src string, shell bool) string {
	lines := strings.Split(src, "\n")
	verbatim := verbatimLines(lines, shell)
	isComment := func(i int) bool {
		t := strings.TrimSpace(lines[i])
		shebang := shell && i == 0 && strings.HasPrefix(t, "#!")
		return !verbatim[i] && !shebang && strings.HasPrefix(t, "#")
	}

	keep := make([]bool, len(lines))
	seenCode := false
	for i := 0; i < len(lines); {
		if isComment(i) {
			end := i
			for end < len(lines) && isComment(end) {
				end++
			}
			drop := !seenCode || end-i >= 2
			for k := i; k < end; k++ {
				keep[k] = !drop
			}
			i = end
			continue
		}
		keep[i] = true
		if t := strings.TrimSpace(lines[i]); t != "" && t != "---" && !strings.HasPrefix(t, "#!") {
			seenCode = true
		}
		i++
	}

	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if !keep[i] {
			continue
		}
		blank := strings.TrimSpace(line) == ""
		prevBlank := len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == ""
		if blank && prevBlank && !verbatim[i] {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	result := strings.Join(out, "\n")
	if strings.HasSuffix(src, "\n") && result != "" {
		result += "\n"
	}
	return result
}

// verbatimLines marks the lines inside heredoc bodies (shell) or block scalars
// (YAML).
func verbatimLines(lines []string, shell bool) []bool {
	verbatim := make([]bool, len(lines))
	if shell {
		terminator := ""
		for i, line := range lines {
			if terminator != "" {
				verbatim[i] = true
				if strings.TrimSpace(line) == terminator {
					terminator = ""
				}
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if m := heredocStart.FindStringSubmatch(line); m != nil {
				terminator = m[1]
			}
		}
		return verbatim
	}

	parentIndent := -1
	for i, line := range lines {
		if parentIndent >= 0 {
			if strings.TrimSpace(line) == "" || indent(line) > parentIndent {
				verbatim[i] = true
				continue
			}
			parentIndent = -1
		}
		if blockScalar.MatchString(line) {
			parentIndent = indent(line)
		}
	}
	return verbatim
}

func indent(line string) int { return len(line) - len(strings.TrimLeft(line, " \t")) }
