package connector

import "regexp"

// TextScanner redacts PII embedded in free text, in place. Layer A is regex
// (high precision, deterministic). An optional LLM/NER layer is added later.
type TextScanner struct {
	patterns []textPattern
}

type textPattern struct {
	kind PiiKind
	re   *regexp.Regexp
}

// NewTextScanner returns the default regex-based scanner.
func NewTextScanner() *TextScanner {
	return &TextScanner{patterns: []textPattern{
		{PiiEmail, regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)},
		{PiiNationalID, regexp.MustCompile(`\b\d{17}[\dXx]\b`)},
		{PiiBankCard, regexp.MustCompile(`\b\d{15,19}\b`)},
		{PiiPhone, regexp.MustCompile(`\b1[3-9]\d{9}\b`)},
		{PiiIP, regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)},
	}}
}

// Scan returns the redacted text and the number of PII spans replaced. Order
// matters: national-id before bank-card before phone to avoid a longer id being
// partially eaten by the phone pattern.
func (s *TextScanner) Scan(text string) (string, int) {
	hits := 0
	for _, p := range s.patterns {
		text = p.re.ReplaceAllStringFunc(text, func(string) string {
			hits++
			return "[REDACTED:" + string(p.kind) + "]"
		})
	}
	return text, hits
}
