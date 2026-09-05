package graph

import (
	"fmt"
	"strings"
	"unicode"
)

// The textual form exists so a rule can be written by a person.
//
//	IF works_at(?x, ?y) AND located_in(?y, ?z) THEN works_in_city(?x, ?z)
//
// The struct form is what programs build; this is what ends up in a config
// file, a tool call, or a message from somebody who does not write Go. They are
// the same rule — Rule.Text renders back into this grammar — so neither form is
// the "real" one.
//
// Keywords are case-insensitive. A term beginning with '?' is a variable;
// anything else is a literal node reference, and may be double-quoted when it
// contains a space, comma or parenthesis.

// RuleParseError names where parsing stopped. A rule is usually typed by hand
// and usually wrong on the first try, so "expected ')'" without a position is
// most of an error message missing.
type RuleParseError struct {
	// Offset is a 0-based byte offset into the source text.
	Offset int
	// Message says what was expected there.
	Message string
	// Source is the text that failed to parse.
	Source string
}

func (e *RuleParseError) Error() string {
	near := strings.TrimSpace(e.Source[min(e.Offset, len(e.Source)):])
	if len(near) > 24 {
		near = near[:24] + "…"
	}
	if near == "" {
		return fmt.Sprintf("rule syntax error at offset %d (end of input): %s", e.Offset, e.Message)
	}
	return fmt.Sprintf("rule syntax error at offset %d (near %q): %s", e.Offset, near, e.Message)
}

// ParseRuleText parses the textual form into a Rule with When and Then filled
// in. ID, Name, Confidence and Metadata are the caller's to set: they are
// bookkeeping about the rule, not part of the logic it states.
func ParseRuleText(text string) (Rule, error) {
	p := &ruleParser{src: text}
	rule, err := p.parse()
	if err != nil {
		return Rule{}, err
	}
	return rule, nil
}

// ParseRule parses the textual form and gives the result an ID, which is the
// common case: a caller with a rule to save has both.
func ParseRule(id, text string) (Rule, error) {
	rule, err := ParseRuleText(text)
	if err != nil {
		return Rule{}, err
	}
	rule.ID = id
	if err := rule.Validate(); err != nil {
		return Rule{}, err
	}
	return rule, nil
}

type ruleParser struct {
	src string
	pos int
}

func (p *ruleParser) errorf(offset int, format string, args ...any) error {
	return &RuleParseError{Offset: offset, Message: fmt.Sprintf(format, args...), Source: p.src}
}

func (p *ruleParser) parse() (Rule, error) {
	var rule Rule
	if err := p.expectKeyword("IF"); err != nil {
		return rule, err
	}
	for {
		atom, err := p.parseAtom()
		if err != nil {
			return rule, err
		}
		rule.When = append(rule.When, atom)

		p.skipSpace()
		switch {
		case p.peekKeyword("AND"):
			_ = p.expectKeyword("AND")
		case p.peekKeyword("THEN"):
			_ = p.expectKeyword("THEN")
			conclusion, err := p.parseAtom()
			if err != nil {
				return rule, err
			}
			rule.Then = conclusion
			p.skipSpace()
			if p.pos != len(p.src) {
				return rule, p.errorf(p.pos, "unexpected input after the conclusion")
			}
			return rule, nil
		default:
			return rule, p.errorf(p.pos, "expected AND or THEN")
		}
	}
}

func (p *ruleParser) skipSpace() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

// peekKeyword reports whether the next word is kw, without consuming it. The
// boundary check matters: "ANDROID(?x, ?y)" is a predicate, not the keyword AND
// followed by nonsense.
func (p *ruleParser) peekKeyword(kw string) bool {
	p.skipSpace()
	if p.pos+len(kw) > len(p.src) {
		return false
	}
	if !strings.EqualFold(p.src[p.pos:p.pos+len(kw)], kw) {
		return false
	}
	next := p.pos + len(kw)
	if next < len(p.src) && isRuleWordByte(p.src[next]) {
		return false
	}
	return true
}

func (p *ruleParser) expectKeyword(kw string) error {
	p.skipSpace()
	if !p.peekKeyword(kw) {
		return p.errorf(p.pos, "expected %s", kw)
	}
	p.pos += len(kw)
	return nil
}

func (p *ruleParser) parseAtom() (Atom, error) {
	var atom Atom
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.src) && isRuleWordByte(p.src[p.pos]) {
		p.pos++
	}
	predicate := p.src[start:p.pos]
	if predicate == "" {
		return atom, p.errorf(start, "expected a predicate name")
	}
	if strings.HasPrefix(predicate, "?") {
		return atom, p.errorf(start, "predicate %q is a variable; predicates must be literal relation types", predicate)
	}
	atom.Predicate = predicate

	p.skipSpace()
	if p.pos >= len(p.src) || p.src[p.pos] != '(' {
		return atom, p.errorf(p.pos, "expected '(' after predicate %q", predicate)
	}
	p.pos++

	subject, err := p.parseTerm()
	if err != nil {
		return atom, err
	}
	atom.Subject = subject

	p.skipSpace()
	if p.pos >= len(p.src) || p.src[p.pos] != ',' {
		return atom, p.errorf(p.pos, "expected ',' between the two terms of %s", predicate)
	}
	p.pos++

	object, err := p.parseTerm()
	if err != nil {
		return atom, err
	}
	atom.Object = object

	p.skipSpace()
	if p.pos >= len(p.src) || p.src[p.pos] != ')' {
		return atom, p.errorf(p.pos, "expected ')' to close %s", predicate)
	}
	p.pos++
	return atom, nil
}

func (p *ruleParser) parseTerm() (string, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return "", p.errorf(p.pos, "expected a term")
	}
	if p.src[p.pos] == '"' {
		return p.parseQuotedTerm()
	}
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != ',' && p.src[p.pos] != ')' {
		p.pos++
	}
	term := strings.TrimSpace(p.src[start:p.pos])
	if term == "" {
		return "", p.errorf(start, "expected a term")
	}
	if term == "?" {
		return "", p.errorf(start, "'?' is not a variable name")
	}
	return term, nil
}

func (p *ruleParser) parseQuotedTerm() (string, error) {
	open := p.pos
	p.pos++ // consume the opening quote
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch c {
		case '\\':
			if p.pos+1 >= len(p.src) {
				return "", p.errorf(p.pos, "trailing escape in a quoted term")
			}
			b.WriteByte(p.src[p.pos+1])
			p.pos += 2
		case '"':
			p.pos++
			if b.Len() == 0 {
				return "", p.errorf(open, "empty quoted term")
			}
			return b.String(), nil
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
	return "", p.errorf(open, "unterminated quoted term")
}

// isRuleWordByte says which bytes may appear bare in a predicate or variable.
// Deliberately generous: relation types in this store are free-form labels, and
// IRIs, prefixed names and node ids all turn up as literals.
func isRuleWordByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c >= 0x80:
		return true
	}
	switch c {
	case '_', '-', ':', '.', '/', '#', '?':
		return true
	}
	return false
}
