package graph

import (
	"fmt"
	"strconv"
	"strings"
)

// miniTurtle parses the Turtle subset the SHACL rule tests are written in:
// @prefix, IRIs, prefixed names, `a`, quoted literals with @lang or ^^type,
// integers, decimals, booleans, [ … ] property lists, ( … ) collections, and
// the ; , . punctuation.
//
// It exists because the Turtle library the importer uses (0x51-dev/rdf
// v0.1.0) gives every predicate-object pair inside [ … ] a fresh blank node of
// its own: `[ a sh:TripleRule ; sh:subject sh:this ]` comes out as two
// unrelated blank nodes, which splits every rule in the SHACL-AF examples
// into pieces. Writing the examples verbatim matters more here than reusing
// that parser.
type miniTurtle struct {
	src      string
	pos      int
	tag      string
	prefixes map[string]string
	out      []RDFTriple
	blanks   int
}

func (p *miniTurtle) rest(n int) string {
	end := p.pos + n
	if end > len(p.src) {
		end = len(p.src)
	}
	return p.src[p.pos:end]
}

func (p *miniTurtle) skip() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '#' {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			p.pos++
			continue
		}
		return
	}
}

func (p *miniTurtle) peek() byte {
	p.skip()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *miniTurtle) expect(c byte) error {
	if p.peek() != c {
		return fmt.Errorf("expected %q", c)
	}
	p.pos++
	return nil
}

func (p *miniTurtle) fresh() RDFTerm {
	p.blanks++
	return NewBlankNode(p.tag + "b" + strconv.Itoa(p.blanks))
}

func (p *miniTurtle) emit(s, pr, o RDFTerm) {
	p.out = append(p.out, RDFTriple{Subject: s, Predicate: pr, Object: o})
}

func (p *miniTurtle) document() error {
	for p.peek() != 0 {
		if strings.HasPrefix(p.src[p.pos:], "@prefix") {
			p.pos += len("@prefix")
			p.skip()
			colon := strings.IndexByte(p.src[p.pos:], ':')
			name := strings.TrimSpace(p.src[p.pos : p.pos+colon])
			p.pos += colon + 1
			iri, err := p.term()
			if err != nil {
				return err
			}
			p.prefixes[name] = iri.Value
			if err := p.expect('.'); err != nil {
				return err
			}
			continue
		}
		subject, err := p.term()
		if err != nil {
			return err
		}
		if p.peek() != '.' {
			if err := p.predicateObjectList(subject); err != nil {
				return err
			}
		}
		if err := p.expect('.'); err != nil {
			return err
		}
	}
	return nil
}

func (p *miniTurtle) predicateObjectList(subject RDFTerm) error {
	for {
		predicate, err := p.term()
		if err != nil {
			return err
		}
		for {
			object, err := p.term()
			if err != nil {
				return err
			}
			p.emit(subject, predicate, object)
			if p.peek() != ',' {
				break
			}
			p.pos++
		}
		if p.peek() != ';' {
			return nil
		}
		for p.peek() == ';' {
			p.pos++
		}
		if c := p.peek(); c == '.' || c == ']' {
			return nil
		}
	}
}

func (p *miniTurtle) term() (RDFTerm, error) {
	c := p.peek()
	switch {
	case c == '<':
		end := strings.IndexByte(p.src[p.pos:], '>')
		iri := p.src[p.pos+1 : p.pos+end]
		p.pos += end + 1
		return NewIRI(iri), nil
	case c == '[':
		p.pos++
		node := p.fresh()
		if p.peek() != ']' {
			if err := p.predicateObjectList(node); err != nil {
				return RDFTerm{}, err
			}
		}
		return node, p.expect(']')
	case c == '(':
		p.pos++
		var items []RDFTerm
		for p.peek() != ')' {
			item, err := p.term()
			if err != nil {
				return RDFTerm{}, err
			}
			items = append(items, item)
		}
		p.pos++
		head := NewIRI(rdfNilIRI)
		cells := make([]RDFTerm, len(items))
		for i := range items {
			cells[i] = p.fresh()
		}
		for i := len(items) - 1; i >= 0; i-- {
			p.emit(cells[i], NewIRI(rdfFirstIRI), items[i])
			p.emit(cells[i], NewIRI(rdfRestIRI), head)
			head = cells[i]
		}
		return head, nil
	case c == '"':
		value := ""
		if strings.HasPrefix(p.src[p.pos:], `"""`) {
			end := strings.Index(p.src[p.pos+3:], `"""`)
			value = p.src[p.pos+3 : p.pos+3+end]
			p.pos += end + 6
		} else {
			end := strings.IndexByte(p.src[p.pos+1:], '"')
			value = p.src[p.pos+1 : p.pos+1+end]
			p.pos += end + 2
		}
		if p.pos < len(p.src) && p.src[p.pos] == '@' {
			start := p.pos + 1
			p.pos++
			for p.pos < len(p.src) && (p.src[p.pos] == '-' || isASCIILetterOrDigit(p.src[p.pos])) {
				p.pos++
			}
			return NewLangLiteral(value, p.src[start:p.pos]), nil
		}
		if strings.HasPrefix(p.src[p.pos:], "^^") {
			p.pos += 2
			dt, err := p.term()
			if err != nil {
				return RDFTerm{}, err
			}
			return NewTypedLiteral(value, dt.Value), nil
		}
		return NewLiteral(value), nil
	case c == '-' || (c >= '0' && c <= '9'):
		start := p.pos
		p.pos++
		for p.pos < len(p.src) {
			ch := p.src[p.pos]
			digitNext := p.pos+1 < len(p.src) && p.src[p.pos+1] >= '0' && p.src[p.pos+1] <= '9'
			if (ch >= '0' && ch <= '9') || (ch == '.' && digitNext) {
				p.pos++
				continue
			}
			break
		}
		lex := p.src[start:p.pos]
		if strings.Contains(lex, ".") {
			return NewTypedLiteral(lex, XSDNamespace+"decimal"), nil
		}
		return NewTypedLiteral(lex, XSDNamespace+"integer"), nil
	}
	start := p.pos
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if strings.IndexByte(" \t\r\n;,[]()", ch) >= 0 {
			break
		}
		if ch == '.' && (p.pos+1 >= len(p.src) || !isASCIILetterOrDigit(p.src[p.pos+1])) {
			break
		}
		p.pos++
	}
	word := p.src[start:p.pos]
	switch word {
	case "a":
		return NewIRI(RDFType), nil
	case "true", "false":
		return NewTypedLiteral(word, XSDNamespace+"boolean"), nil
	case "":
		return RDFTerm{}, fmt.Errorf("expected a term")
	}
	if strings.HasPrefix(word, "_:") {
		return NewBlankNode(p.tag + word[2:]), nil
	}
	colon := strings.IndexByte(word, ':')
	if colon < 0 {
		return RDFTerm{}, fmt.Errorf("unknown token %q", word)
	}
	ns, ok := p.prefixes[word[:colon]]
	if !ok {
		return RDFTerm{}, fmt.Errorf("unknown prefix in %q", word)
	}
	return NewIRI(ns + word[colon+1:]), nil
}

func isASCIILetterOrDigit(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
