package runner

import (
	"errors"
	"strings"
)

var (
	errRepetitionLoop = errors.New("repetition loop detected")
	errManualSkip     = errors.New("manually skipped")
	errManualRetry    = errors.New("manually retried")
	errManualSkipModel = errors.New("manually skipped model")
	// errAutoSkipStage aborts a turn when a configured per-stage testing
	// limit is exceeded. It wraps details about the stage and the limit;
	// callers match it with errors.Is to treat the turn as skipped.
	errAutoSkipStage = errors.New("auto-skipped")
)

// isUniformChar reports whether s is non-empty and composed entirely of the same rune.
func isUniformChar(s string) bool {
	if len(s) == 0 {
		return true
	}
	runes := []rune(s)
	r0 := runes[0]
	for _, r := range runes[1:] {
		if r != r0 {
			return false
		}
	}
	return true
}

// detectRepetitionLoop checks if the text ends with a repetitive degenerative sequence.
// It scans suffix patterns of length p from 1 to 64 runes.
// Returns true and the repeating pattern if a loop is detected.
func detectRepetitionLoop(s string) (bool, string) {
	if len(s) < 20 {
		return false, ""
	}
	// Inspect up to the last 600 characters
	tail := s
	if len(tail) > 600 {
		tail = tail[len(tail)-600:]
	}

	tailRunes := []rune(tail)
	n := len(tailRunes)

	// Check pattern lengths p from 1 to 64 runes
	for p := 1; p <= 64 && p*4 <= n; p++ {
		patternRunes := tailRunes[n-p:]
		pattern := string(patternRunes)

		var minK int
		if p == 1 {
			r := patternRunes[0]
			// Only flag p=1 for letters or digits repeating 40+ times (not dashes, dots, spaces, etc.)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				minK = 40
			} else {
				continue
			}
		} else {
			// Skip patterns that are just uniform identical chars (e.g. "----", "====")
			if isUniformChar(pattern) {
				continue
			}
			switch {
			case p == 2:
				minK = 16
			case p <= 6:
				minK = 10
			case p <= 15:
				minK = 7
			default:
				minK = 4
			}
		}

		neededLen := p * minK
		if n < neededLen {
			continue
		}

		matched := true
		for i := 0; i < minK; i++ {
			chunk := string(tailRunes[n-neededLen+i*p : n-neededLen+(i+1)*p])
			if chunk != pattern {
				matched = false
				break
			}
		}

		if matched {
			return true, pattern
		}
	}

	return false, ""
}

// isLoopOrSkip reports whether the error string indicates a repetition loop,
// a manual test skip or an automatic stage-limit skip.
func isLoopOrSkip(errStr string) bool {
	return strings.Contains(errStr, "repetition loop detected") || strings.Contains(errStr, "manually skipped") || strings.Contains(errStr, "auto-skipped")
}
