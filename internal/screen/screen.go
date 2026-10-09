// Package screen reads Claude Code's prompt box and footer from a herdr pane read (format "ansi").
package screen

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// placeholderSGR are the SGR parameters Claude uses for suggestion text in an empty box.
var placeholderSGR = map[string]bool{"2": true, "90": true}

// busyPatterns match the footer while background work runs.
var busyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b\d+\s+(background|bash|shells?)\b`),
	regexp.MustCompile(`(?i)\bbackground (task|shell|agent)s?\b`),
}

var sgr = regexp.MustCompile("\x1b\\[([0-9;]*)m")
var otherCSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-ln-z]")

// visibleText drops escape codes and, when dropPlaceholder, the text drawn in a placeholder style.
func visibleText(s string, dropPlaceholder bool) string {
	s = otherCSI.ReplaceAllString(s, "")
	var b strings.Builder
	faint := false
	for len(s) > 0 {
		loc := sgr.FindStringSubmatchIndex(s)
		if loc == nil {
			if !(dropPlaceholder && faint) {
				b.WriteString(s)
			}
			break
		}
		if !(dropPlaceholder && faint) {
			b.WriteString(s[:loc[0]])
		}
		for _, p := range strings.Split(s[loc[2]:loc[3]], ";") {
			switch {
			case placeholderSGR[p]:
				faint = true
			case p == "" || p == "0" || p == "22" || p == "39":
				faint = false
			}
		}
		s = s[loc[1]:]
	}
	return b.String()
}

func isRule(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "───") }

// layout finds the prompt box: the last line starting with ❯ that sits below a rule, and the rule under it.
func layout(lines []string) (top, prompt, bottom int, ok bool) {
	for i := len(lines) - 1; i > 0; i-- {
		if !strings.HasPrefix(strings.TrimSpace(visibleText(lines[i], false)), "❯") {
			continue
		}
		if !isRule(visibleText(lines[i-1], false)) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if isRule(visibleText(lines[j], false)) {
				return i - 1, i, j, true
			}
		}
	}
	return 0, 0, 0, false
}

func Draft(ansiText string) (string, bool) {
	lines := strings.Split(ansiText, "\n")
	_, p, bottom, ok := layout(lines)
	if !ok {
		return "", false
	}
	var parts []string
	for i := p; i < bottom; i++ {
		t := visibleText(lines[i], true)
		if i == p {
			t = strings.TrimPrefix(strings.TrimSpace(t), "❯")
		}
		if t = strings.TrimSpace(t); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n"), true
}

func Fingerprint(ansiText string) string {
	lines := strings.Split(ansiText, "\n")
	if _, _, bottom, ok := layout(lines); ok {
		lines = lines[:bottom]
	}
	sum := sha256.Sum256([]byte(visibleText(strings.Join(lines, "\n"), false)))
	return hex.EncodeToString(sum[:])
}

func Busy(ansiText string) bool {
	lines := strings.Split(ansiText, "\n")
	_, _, bottom, ok := layout(lines)
	if !ok {
		return false
	}
	foot := visibleText(strings.Join(lines[bottom+1:], "\n"), false)
	for _, re := range busyPatterns {
		if re.MatchString(foot) {
			return true
		}
	}
	return false
}
