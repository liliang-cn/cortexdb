package connector

import (
	"strconv"
	"strings"
)

// Redact replaces a value with a fixed irreversible marker.
func Redact(string) string { return "[REDACTED]" }

// MaskValue partially hides a value based on its PII kind, keeping enough for
// humans to recognize but not enough to identify. Irreversible.
func MaskValue(kind PiiKind, v string) string {
	if v == "" {
		return ""
	}
	switch kind {
	case PiiPhone:
		return keepEnds(v, 3, 4)
	case PiiBankCard:
		return keepEnds(v, 4, 4)
	case PiiNationalID:
		return keepEnds(v, 6, 4)
	case PiiEmail:
		at := strings.IndexByte(v, '@')
		if at <= 1 {
			return "***" + v[max(at, 0):]
		}
		return v[:1] + "***" + v[at:]
	case PiiName:
		r := []rune(v)
		if len(r) <= 1 {
			return v
		}
		return string(r[:1]) + strings.Repeat("*", len(r)-1)
	default:
		return strings.Repeat("*", runeLen(v))
	}
}

// keepEnds keeps the first `head` and last `tail` runes, starring the middle.
// When the value is too short to keep both ends, everything is starred.
func keepEnds(v string, head, tail int) string {
	r := []rune(v)
	if len(r) <= head+tail {
		return strings.Repeat("*", len(r))
	}
	return string(r[:head]) + strings.Repeat("*", len(r)-head-tail) + string(r[len(r)-tail:])
}

// GeneralizeValue buckets a value to reduce identifiability. Irreversible.
func GeneralizeValue(kind PiiKind, v string) string {
	switch kind {
	case PiiDOB:
		// keep year-month, drop day: 1990-03-07 -> 1990-03
		if len(v) >= 7 {
			return v[:7]
		}
		return v
	default:
		return GeneralizeAge(v)
	}
}

// GeneralizeAge buckets an integer age into a decade band; non-ints pass through.
func GeneralizeAge(v string) string {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return v
	}
	lo := (n / 10) * 10
	return strconv.Itoa(lo) + "-" + strconv.Itoa(lo+10)
}

func runeLen(s string) int { return len([]rune(s)) }
