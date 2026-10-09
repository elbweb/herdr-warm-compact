// Package screen reads Claude Code's prompt box and footer from a herdr pane read (format "ansi").
package screen

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// busyPatterns match the footer while background work runs.
var busyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b\d+\s+(background|bash|shells?)\b`),
	regexp.MustCompile(`(?i)\bbackground (task|shell|agent)s?\b`),
}

// sgr matches a Select Graphic Rendition sequence; parameters may use ':' sub-parameters.
var sgr = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)
var otherCSI = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-ln-z]`)

// osc matches an operating system command (hyperlinks, titles), ended by BEL or ST.
var osc = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// sgrState tracks the two styles that mark suggestion text: faint (2) and grey (90).
type sgrState struct {
	faint, grey bool
}

func (st *sgrState) placeholder() bool { return st.faint || st.grey }

// apply walks the ';'-separated parameters in order. Extended colour arguments
// (38/48/58 followed by 5;n or 2;r;g;b) are skipped, so their values are never read as codes.
func (st *sgrState) apply(params string) {
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if strings.Contains(p, ":") {
			// Colon form: one colour parameter group, never faint.
			if strings.HasPrefix(p, "38:") {
				st.grey = false
			}
			continue
		}
		n := 0
		if p != "" {
			var err error
			if n, err = strconv.Atoi(p); err != nil {
				continue
			}
		}
		switch {
		case n == 0:
			st.faint, st.grey = false, false
		case n == 2:
			st.faint = true
		case n == 22:
			st.faint = false
		case n == 90:
			st.grey = true
		case n == 39 || (n >= 30 && n <= 37) || (n >= 91 && n <= 97):
			st.grey = false
		case n == 38:
			st.grey = false
			i = skipColour(parts, i)
		case n == 48 || n == 58:
			i = skipColour(parts, i)
		}
	}
}

// skipColour returns the index of the last argument of an extended colour starting at i.
func skipColour(parts []string, i int) int {
	if i+1 < len(parts) {
		switch parts[i+1] {
		case "5":
			return i + 2
		case "2":
			return i + 4
		}
	}
	return i
}

// visibleText drops OSC and other escape codes and, when dropPlaceholder, the text drawn in a placeholder style.
func visibleText(s string, dropPlaceholder bool) string {
	s = osc.ReplaceAllString(s, "")
	s = otherCSI.ReplaceAllString(s, "")
	var b strings.Builder
	var st sgrState
	for len(s) > 0 {
		loc := sgr.FindStringSubmatchIndex(s)
		if loc == nil {
			if !(dropPlaceholder && st.placeholder()) {
				b.WriteString(s)
			}
			break
		}
		if !(dropPlaceholder && st.placeholder()) {
			b.WriteString(s[:loc[0]])
		}
		st.apply(s[loc[2]:loc[3]])
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
