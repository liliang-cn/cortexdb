package cypher

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tQuotedIdent // `backticked`, never a keyword
	tString
	tInt
	tFloat
	tParam
	tPunct
)

type token struct {
	kind tokKind
	text string // identifier / punctuation / raw number text; decoded string body
	pos  int
}

func (t token) String() string {
	switch t.kind {
	case tEOF:
		return "end of query"
	case tString:
		return fmt.Sprintf("'%s'", t.text)
	case tParam:
		return "$" + t.text
	}
	return t.text
}

// lex splits a query into tokens.
//
// Punctuation is emitted one character at a time except for the handful of
// operators that are never ambiguous (<=, >=, <>, =~, .., +=). Arrows are
// deliberately not tokens: `<--` would otherwise be read as `<-` `-` in one
// place and `<` `--` in another, and the pattern parser is the only part that
// knows which it is.
func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		r, w := utf8.DecodeRuneInString(src[i:])
		switch {
		case r == utf8.RuneError && w == 1:
			return nil, &Error{Kind: ErrSyntax, Pos: i, Msg: "invalid UTF-8 in query"}
		case unicode.IsSpace(r):
			i += w
		case r == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case r == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, &Error{Kind: ErrSyntax, Pos: i, Msg: "unterminated comment"}
			}
			i += end + 4
		case r == '\'' || r == '"':
			s, n, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			out = append(out, token{kind: tString, text: s, pos: i})
			i += n
		case r == '`':
			start := i
			i++
			var b strings.Builder
			for {
				if i >= len(src) {
					return nil, &Error{Kind: ErrSyntax, Pos: start, Msg: "unterminated `quoted` name"}
				}
				if src[i] == '`' {
					if i+1 < len(src) && src[i+1] == '`' {
						b.WriteByte('`')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(src[i])
				i++
			}
			out = append(out, token{kind: tQuotedIdent, text: b.String(), pos: start})
		case r == '$':
			start := i
			i++
			j := i
			for j < len(src) {
				c, cw := utf8.DecodeRuneInString(src[j:])
				if c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c) {
					j += cw
					continue
				}
				break
			}
			if j == i {
				return nil, &Error{Kind: ErrSyntax, Pos: start, Msg: "a parameter needs a name after $"}
			}
			out = append(out, token{kind: tParam, text: src[i:j], pos: start})
			i = j
		case r >= '0' && r <= '9' || (r == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9' && !prevIsDotDot(out)):
			tok, n, err := lexNumber(src, i)
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
			i += n
		case r == '_' || unicode.IsLetter(r):
			start := i
			for i < len(src) {
				c, cw := utf8.DecodeRuneInString(src[i:])
				if c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c) {
					i += cw
					continue
				}
				break
			}
			out = append(out, token{kind: tIdent, text: src[start:i], pos: start})
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "<=", ">=", "<>", "!=", "=~", "..", "+=":
				out = append(out, token{kind: tPunct, text: two, pos: i})
				i += 2
				continue
			}
			if strings.ContainsRune("()[]{}:,.;|=<>-+*/%^", r) {
				out = append(out, token{kind: tPunct, text: string(r), pos: i})
				i += w
				continue
			}
			return nil, &Error{Kind: ErrSyntax, Pos: i, Msg: fmt.Sprintf("unexpected character %q", r)}
		}
	}
	out = append(out, token{kind: tEOF, pos: len(src)})
	return out, nil
}

// prevIsDotDot keeps `*1..3` lexing as 1, .., 3 rather than 1, .3-ish.
func prevIsDotDot(out []token) bool {
	return len(out) > 0 && out[len(out)-1].kind == tPunct && out[len(out)-1].text == ".."
}

func lexNumber(src string, i int) (token, int, error) {
	start := i
	if src[i] == '0' && i+1 < len(src) && (src[i+1] == 'x' || src[i+1] == 'X' || src[i+1] == 'o' || src[i+1] == 'O') {
		j := i + 2
		for j < len(src) && isAlnum(src[j]) {
			j++
		}
		return token{kind: tInt, text: src[start:j], pos: start}, j - start, nil
	}
	isFloat := false
	for i < len(src) && src[i] >= '0' && src[i] <= '9' {
		i++
	}
	// A dot followed by a digit is a fraction; `1..3` is a range.
	if i+1 < len(src) && src[i] == '.' && src[i+1] >= '0' && src[i+1] <= '9' {
		isFloat = true
		i++
		for i < len(src) && src[i] >= '0' && src[i] <= '9' {
			i++
		}
	}
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		j := i + 1
		if j < len(src) && (src[j] == '+' || src[j] == '-') {
			j++
		}
		if j < len(src) && src[j] >= '0' && src[j] <= '9' {
			isFloat = true
			i = j
			for i < len(src) && src[i] >= '0' && src[i] <= '9' {
				i++
			}
		}
	}
	// 123abc is not a number followed by a name.
	if i < len(src) && (isAlnum(src[i]) || src[i] == '_') {
		return token{}, 0, &Error{Kind: ErrSyntax, Pos: start, Msg: fmt.Sprintf("invalid number literal near %q", src[start:min(len(src), i+1)])}
	}
	k := tInt
	if isFloat {
		k = tFloat
	}
	return token{kind: k, text: src[start:i], pos: start}, i - start, nil
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func lexString(src string, i int) (string, int, error) {
	quote := src[i]
	start := i
	i++
	var b strings.Builder
	for {
		if i >= len(src) {
			return "", 0, &Error{Kind: ErrSyntax, Pos: start, Msg: "unterminated string literal"}
		}
		c := src[i]
		if c == quote {
			i++
			break
		}
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(src) {
			return "", 0, &Error{Kind: ErrSyntax, Pos: i, Msg: "unterminated escape in string literal"}
		}
		e := src[i+1]
		i += 2
		switch e {
		case '\\', '\'', '"':
			b.WriteByte(e)
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'u', 'U':
			n := 4
			if e == 'U' {
				n = 8
			}
			if i+n > len(src) {
				return "", 0, &Error{Kind: ErrSyntax, Pos: i, Msg: "short unicode escape"}
			}
			var cp rune
			for _, h := range src[i : i+n] {
				v := hexVal(h)
				if v < 0 {
					return "", 0, &Error{Kind: ErrSyntax, Pos: i, Msg: "invalid unicode escape"}
				}
				cp = cp*16 + rune(v)
			}
			if !utf8.ValidRune(cp) {
				return "", 0, &Error{Kind: ErrSyntax, Pos: i, Msg: "invalid unicode escape"}
			}
			b.WriteRune(cp)
			i += n
		default:
			return "", 0, &Error{Kind: ErrSyntax, Pos: i - 2, Msg: fmt.Sprintf("unknown escape \\%c", e)}
		}
	}
	return b.String(), i - start, nil
}

func hexVal(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}
