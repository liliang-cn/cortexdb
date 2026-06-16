package connector

import (
	"context"
	"regexp"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// Classifier proposes a PII kind + sensitivity for a column from its name, type,
// and a few SAMPLE values (never the full column).
type Classifier interface {
	Classify(ctx context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string)
}

var (
	reEmail    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	rePhoneCN  = regexp.MustCompile(`^1[3-9]\d{9}$`)
	reNatIDCN  = regexp.MustCompile(`^\d{17}[\dXx]$`)
	reBankCard = regexp.MustCompile(`^\d{15,19}$`)
	reIP       = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)
)

// nameHints maps substrings in a column name to a PII kind.
var nameHints = []struct {
	sub  string
	kind PiiKind
}{
	{"email", PiiEmail}, {"mail", PiiEmail},
	{"phone", PiiPhone}, {"mobile", PiiPhone}, {"tel", PiiPhone},
	{"id_card", PiiNationalID}, {"idcard", PiiNationalID}, {"national", PiiNationalID}, {"ssn", PiiNationalID},
	{"bank", PiiBankCard}, {"card_no", PiiBankCard}, {"cardno", PiiBankCard},
	{"name", PiiName},
	{"addr", PiiAddress}, {"address", PiiAddress},
	{"birth", PiiDOB}, {"dob", PiiDOB},
	{"ip", PiiIP},
}

// defaultSensitivity assigns a level per kind.
func defaultSensitivity(k PiiKind) Sensitivity {
	switch k {
	case PiiNationalID, PiiBankCard:
		return Restricted
	case PiiPhone, PiiEmail, PiiAddress, PiiDOB:
		return Confidential
	case PiiName, PiiIP, PiiGeo:
		return Internal
	default:
		return Public
	}
}

// RuleClassifier classifies by column-name hints, then by value regex.
type RuleClassifier struct{}

// NewRuleClassifier returns a deterministic, dependency-free classifier.
func NewRuleClassifier() *RuleClassifier { return &RuleClassifier{} }

// Classify implements Classifier. Value-regex evidence overrides a weak name guess.
func (c *RuleClassifier) Classify(_ context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string) {
	// 1) value regex (strongest signal — actual data shape)
	if k := classifyByValues(samples); k != PiiNone {
		return k, defaultSensitivity(k), "rule:value-regex"
	}
	// 2) column-name hint
	name := strings.ToLower(col.Name)
	for _, h := range nameHints {
		if strings.Contains(name, h.sub) {
			return h.kind, defaultSensitivity(h.kind), "rule:name:" + h.sub
		}
	}
	return PiiNone, Public, ""
}

// classifyByValues returns a kind only if a strong majority of non-empty samples
// match one pattern (avoids a single coincidental match).
func classifyByValues(samples []string) PiiKind {
	type counter struct {
		kind PiiKind
		re   *regexp.Regexp
	}
	counters := []counter{
		{PiiEmail, reEmail}, {PiiPhone, rePhoneCN}, {PiiNationalID, reNatIDCN}, {PiiIP, reIP}, {PiiBankCard, reBankCard},
	}
	nonEmpty := 0
	hits := map[PiiKind]int{}
	for _, s := range samples {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		nonEmpty++
		for _, c := range counters {
			if c.re.MatchString(s) {
				hits[c.kind]++
				break
			}
		}
	}
	if nonEmpty == 0 {
		return PiiNone
	}
	for _, c := range counters {
		if hits[c.kind]*2 > nonEmpty { // strict majority
			return c.kind
		}
	}
	return PiiNone
}
