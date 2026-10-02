package graph

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	mathrand "math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// This file is the SPARQL 1.1 expression layer: the operators, the built-in
// function library, and the error model they share.
//
// The error model is the part that matters most. SPARQL distinguishes a query
// that is wrong from a solution that cannot be evaluated: STRLEN of an IRI is
// not a broken query, it is one row for which the expression has no value. A
// FILTER treats that as false and drops the row; a BIND or SELECT expression
// leaves the variable unbound. Returning a query-level error there would let a
// single odd value in the data fail every query that touches it, so every
// per-row failure is a *sparqlExprError, and only the places that know the
// spec's rule for their clause swallow it. Anything else (a storage failure
// inside EXISTS, a cancelled context) is an ordinary error and aborts.

const (
	// sparqlMaxRegexPatternBytes bounds a user-supplied REGEX/REPLACE
	// pattern. RE2 cannot backtrack catastrophically, but compiling a
	// megabyte pattern per row is still a denial of service.
	sparqlMaxRegexPatternBytes = 4096
	// sparqlMaxCachedRegexes caps the per-query cache, so a pattern taken
	// from the data (a different one per row) cannot grow it without bound.
	sparqlMaxCachedRegexes = 256
)

// xsdStringIRI and rdfLangStringIRI are declared beside the SHACL datatype
// checks, which need them too.
const (
	xsdBooleanIRI      = XSDNamespace + "boolean"
	xsdIntegerIRI      = XSDNamespace + "integer"
	xsdDecimalIRI      = XSDNamespace + "decimal"
	xsdFloatIRI        = XSDNamespace + "float"
	xsdDoubleIRI       = XSDNamespace + "double"
	xsdDateTimeIRI     = XSDNamespace + "dateTime"
	xsdDayTimeDuration = XSDNamespace + "dayTimeDuration"
)

type sparqlExprError struct {
	msg string
}

func (e *sparqlExprError) Error() string { return e.msg }

func sparqlTypeErrorf(format string, args ...any) error {
	return &sparqlExprError{msg: fmt.Sprintf(format, args...)}
}

func isSPARQLExprError(err error) bool {
	var exprErr *sparqlExprError
	return errors.As(err, &exprErr)
}

// errSPARQLUnbound is what an operator sees when an argument has no value.
// The spec folds "unbound" into "error" for every operator except BOUND,
// COALESCE and IF, which never evaluate the unbound argument this way.
var errSPARQLUnbound = &sparqlExprError{msg: "unbound variable"}

// sparqlRuntime is the per-query state expressions need but a solution does
// not carry: the store for EXISTS, the dataset, the query's single NOW(), and
// the caches that make REGEX and BNODE(str) behave per query rather than per
// row. The parser creates it and every node that needs it holds a pointer, so
// the value-expression interface stays a pure function of the binding.
type sparqlRuntime struct {
	ctx   context.Context
	store *GraphStore
	opts  sparqlExecOptions

	now   RDFTerm
	nonce string

	mu       sync.Mutex
	regexes  map[string]*regexp.Regexp
	bnodes   map[string]RDFTerm
	bnodeSeq int
}

func newSPARQLRuntime() *sparqlRuntime {
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	return &sparqlRuntime{
		ctx:     context.Background(),
		now:     NewTypedLiteral(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), xsdDateTimeIRI),
		nonce:   hex.EncodeToString(nonce[:]),
		regexes: make(map[string]*regexp.Regexp),
		bnodes:  make(map[string]RDFTerm),
	}
}

func (rt *sparqlRuntime) freshBlankNode() RDFTerm {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.bnodeSeq++
	return NewBlankNode(fmt.Sprintf("q%s_%d", rt.nonce, rt.bnodeSeq))
}

func (rt *sparqlRuntime) labelledBlankNode(label string) RDFTerm {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if node, ok := rt.bnodes[label]; ok {
		return node
	}
	rt.bnodeSeq++
	node := NewBlankNode(fmt.Sprintf("q%s_%d", rt.nonce, rt.bnodeSeq))
	rt.bnodes[label] = node
	return node
}

// compileRegex applies the XPath flags SPARQL defines (s, m, i, x, q) and
// caches the result for the rest of the query.
func (rt *sparqlRuntime) compileRegex(pattern, flags string) (*regexp.Regexp, error) {
	if len(pattern) > sparqlMaxRegexPatternBytes {
		return nil, sparqlTypeErrorf("regex pattern longer than %d bytes", sparqlMaxRegexPatternBytes)
	}
	key := flags + "\x00" + pattern
	rt.mu.Lock()
	cached, ok := rt.regexes[key]
	rt.mu.Unlock()
	if ok {
		return cached, nil
	}
	goFlags := ""
	for _, flag := range flags {
		switch flag {
		case 'i', 'm', 's':
			if !strings.ContainsRune(goFlags, flag) {
				goFlags += string(flag)
			}
		case 'x', 'q':
		default:
			return nil, sparqlTypeErrorf("invalid regex flag %q", flag)
		}
	}
	expr := pattern
	switch {
	case strings.ContainsRune(flags, 'q'):
		expr = regexp.QuoteMeta(pattern)
	case strings.ContainsRune(flags, 'x'):
		expr = stripRegexWhitespace(pattern)
	}
	if goFlags != "" {
		expr = "(?" + goFlags + ")" + expr
	}
	compiled, err := regexp.Compile(expr)
	if err != nil {
		return nil, sparqlTypeErrorf("invalid regex: %v", err)
	}
	rt.mu.Lock()
	if len(rt.regexes) < sparqlMaxCachedRegexes {
		rt.regexes[key] = compiled
	}
	rt.mu.Unlock()
	return compiled, nil
}

// stripRegexWhitespace implements the XPath "x" flag: whitespace outside a
// character class is not part of the pattern.
func stripRegexWhitespace(pattern string) string {
	var out strings.Builder
	inClass := false
	escaped := false
	for _, r := range pattern {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '[':
			inClass = true
		case r == ']':
			inClass = false
		case !inClass && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// ---------------------------------------------------------------------------
// Expression nodes
// ---------------------------------------------------------------------------

// sparqlEvalFn evaluates a sub-expression in the caller's mode — against one
// solution, or against a group — so each operator is written once.
type sparqlEvalFn func(sparqlValueExpr) (RDFTerm, bool, error)

func evalSingle(binding map[string]RDFTerm) sparqlEvalFn {
	return func(e sparqlValueExpr) (RDFTerm, bool, error) { return e.Eval(binding) }
}

func evalGrouped(bindings []map[string]RDFTerm) sparqlEvalFn {
	return func(e sparqlValueExpr) (RDFTerm, bool, error) { return e.EvalGroup(bindings) }
}

// evalValue evaluates an operand an operator needs a value for, turning
// "unbound" into the error the spec says it is.
func evalValue(eval sparqlEvalFn, expr sparqlValueExpr) (RDFTerm, error) {
	value, ok, err := eval(expr)
	if err != nil {
		return RDFTerm{}, err
	}
	if !ok {
		return RDFTerm{}, errSPARQLUnbound
	}
	return value, nil
}

// evalBoolean evaluates an operand's effective boolean value.
func evalBoolean(eval sparqlEvalFn, expr sparqlValueExpr) (bool, error) {
	value, err := evalValue(eval, expr)
	if err != nil {
		return false, err
	}
	return effectiveBooleanValue(value)
}

func booleanTerm(value bool) RDFTerm {
	return NewTypedLiteral(strconv.FormatBool(value), xsdBooleanIRI)
}

type sparqlLogicalExpr struct {
	Op    string // "&&" or "||"
	Left  sparqlValueExpr
	Right sparqlValueExpr
}

type sparqlNotExpr struct {
	Inner sparqlValueExpr
}

type sparqlCompareExpr struct {
	Op    string
	Left  sparqlValueExpr
	Right sparqlValueExpr
}

type sparqlInExpr struct {
	Value   sparqlValueExpr
	List    []sparqlValueExpr
	Negated bool
}

type sparqlBoundExpr struct {
	Variable string
}

type sparqlExistsExpr struct {
	Group   sparqlGroup
	Negated bool
	rt      *sparqlRuntime
}

type sparqlFuncExpr struct {
	Name string
	Args []sparqlValueExpr
	fn   *sparqlFunction
	rt   *sparqlRuntime
}

// eval is SPARQL's three-valued logic: an error on one side of || is
// hidden by a true on the other, and on one side of && by a false.
func (e sparqlLogicalExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	left, leftErr := evalBoolean(eval, e.Left)
	if leftErr != nil && !isSPARQLExprError(leftErr) {
		return RDFTerm{}, false, leftErr
	}
	decisive := e.Op == "||"
	if leftErr == nil && left == decisive {
		return booleanTerm(decisive), true, nil
	}
	right, rightErr := evalBoolean(eval, e.Right)
	if rightErr != nil && !isSPARQLExprError(rightErr) {
		return RDFTerm{}, false, rightErr
	}
	if rightErr == nil && right == decisive {
		return booleanTerm(decisive), true, nil
	}
	if leftErr != nil {
		return RDFTerm{}, false, leftErr
	}
	if rightErr != nil {
		return RDFTerm{}, false, rightErr
	}
	return booleanTerm(!decisive), true, nil
}

func (e sparqlLogicalExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalSingle(binding))
}

func (e sparqlLogicalExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
}

func (e sparqlLogicalExpr) IsAggregate() bool { return e.Left.IsAggregate() || e.Right.IsAggregate() }

func (e sparqlNotExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	value, err := evalBoolean(eval, e.Inner)
	if err != nil {
		return RDFTerm{}, false, err
	}
	return booleanTerm(!value), true, nil
}

func (e sparqlNotExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalSingle(binding))
}

func (e sparqlNotExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
}

func (e sparqlNotExpr) IsAggregate() bool { return e.Inner.IsAggregate() }

func (e sparqlCompareExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	left, err := evalValue(eval, e.Left)
	if err != nil {
		return RDFTerm{}, false, err
	}
	right, err := evalValue(eval, e.Right)
	if err != nil {
		return RDFTerm{}, false, err
	}
	result, err := sparqlCompareOp(e.Op, left, right)
	if err != nil {
		return RDFTerm{}, false, err
	}
	return booleanTerm(result), true, nil
}

func (e sparqlCompareExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalSingle(binding))
}

func (e sparqlCompareExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
}

func (e sparqlCompareExpr) IsAggregate() bool { return e.Left.IsAggregate() || e.Right.IsAggregate() }

// IN is a disjunction of = tests: true if any is true, an error if none is
// true and one of them erred, false otherwise.
func (e sparqlInExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	value, err := evalValue(eval, e.Value)
	if err != nil {
		return RDFTerm{}, false, err
	}
	var firstErr error
	for _, candidateExpr := range e.List {
		candidate, err := evalValue(eval, candidateExpr)
		if err == nil {
			var equal bool
			equal, err = sparqlCompareOp("=", value, candidate)
			if err == nil && equal {
				return booleanTerm(!e.Negated), true, nil
			}
		}
		if err != nil {
			if !isSPARQLExprError(err) {
				return RDFTerm{}, false, err
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return RDFTerm{}, false, firstErr
	}
	return booleanTerm(e.Negated), true, nil
}

func (e sparqlInExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalSingle(binding))
}

func (e sparqlInExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
}

func (e sparqlInExpr) IsAggregate() bool {
	if e.Value.IsAggregate() {
		return true
	}
	for _, item := range e.List {
		if item.IsAggregate() {
			return true
		}
	}
	return false
}

func (e sparqlBoundExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	_, ok := binding[e.Variable]
	return booleanTerm(ok), true, nil
}

func (e sparqlBoundExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	if len(bindings) == 0 {
		return booleanTerm(false), true, nil
	}
	return e.Eval(bindings[0])
}

func (e sparqlBoundExpr) IsAggregate() bool { return false }

func (e sparqlExistsExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	if e.rt == nil || e.rt.store == nil {
		return RDFTerm{}, false, fmt.Errorf("EXISTS/NOT EXISTS requires SPARQL execution context")
	}
	matches, err := e.rt.store.executeSPARQLGroup(e.rt.ctx, e.Group, []map[string]RDFTerm{cloneBinding(binding)}, e.rt.opts)
	if err != nil {
		return RDFTerm{}, false, err
	}
	return booleanTerm((len(matches) > 0) != e.Negated), true, nil
}

func (e sparqlExistsExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	if len(bindings) == 0 {
		return booleanTerm(e.Negated), true, nil
	}
	return e.Eval(bindings[0])
}

func (e sparqlExistsExpr) IsAggregate() bool { return false }

func (e sparqlFuncExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	args := make([]RDFTerm, len(e.Args))
	for i, argExpr := range e.Args {
		value, err := evalValue(eval, argExpr)
		if err != nil {
			return RDFTerm{}, false, err
		}
		args[i] = value
	}
	value, err := e.fn.eval(e.rt, args)
	if err != nil {
		return RDFTerm{}, false, err
	}
	return value, true, nil
}

func (e sparqlFuncExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalSingle(binding))
}

func (e sparqlFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
}

func (e sparqlFuncExpr) IsAggregate() bool {
	for _, arg := range e.Args {
		if arg.IsAggregate() {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Term classification, numbers, comparison
// ---------------------------------------------------------------------------

func isLangLiteral(term RDFTerm) bool {
	return term.Kind == RDFTermLiteral && term.Language != ""
}

// isXSDStringLiteral is a simple literal or an xsd:string; RDF 1.1 makes the
// two the same thing, and this store writes the former.
func isXSDStringLiteral(term RDFTerm) bool {
	return term.Kind == RDFTermLiteral && term.Language == "" && (term.Datatype == "" || term.Datatype == xsdStringIRI)
}

// isStringLiteral is the spec's "string literal": xsd:string or
// language-tagged, the argument type of the string functions.
func isStringLiteral(term RDFTerm) bool {
	return isXSDStringLiteral(term) || isLangLiteral(term)
}

func requireStringLiteral(name string, term RDFTerm) error {
	if !isStringLiteral(term) {
		return sparqlTypeErrorf("%s requires a string literal", name)
	}
	return nil
}

func requireSimpleString(name string, term RDFTerm) error {
	if !isXSDStringLiteral(term) {
		return sparqlTypeErrorf("%s requires a simple literal or xsd:string", name)
	}
	return nil
}

// argumentsCompatible is SPARQL 1.1 §17.4.3.1.1: two plain strings, two
// strings with the same language tag, or a tagged first argument with a plain
// second one.
func argumentsCompatible(name string, first, second RDFTerm) error {
	if err := requireStringLiteral(name, first); err != nil {
		return err
	}
	if err := requireStringLiteral(name, second); err != nil {
		return err
	}
	if isXSDStringLiteral(second) {
		return nil
	}
	if isLangLiteral(first) && strings.EqualFold(first.Language, second.Language) {
		return nil
	}
	return sparqlTypeErrorf("%s arguments are not compatible", name)
}

// stringLike builds a result string carrying the language tag or datatype of
// the argument it was derived from, as UCASE, SUBSTR and friends require.
func stringLike(value string, like RDFTerm) RDFTerm {
	return RDFTerm{Kind: RDFTermLiteral, Value: value, Language: like.Language, Datatype: like.Datatype}
}

const (
	sparqlNumInteger = iota
	sparqlNumDecimal
	sparqlNumFloat
	sparqlNumDouble
)

type sparqlNumber struct {
	kind  int
	value float64
}

func sparqlNumericKind(datatype string) (int, bool) {
	if !strings.HasPrefix(datatype, XSDNamespace) {
		return 0, false
	}
	switch strings.TrimPrefix(datatype, XSDNamespace) {
	case "integer", "int", "long", "short", "byte",
		"nonNegativeInteger", "positiveInteger", "nonPositiveInteger", "negativeInteger",
		"unsignedLong", "unsignedInt", "unsignedShort", "unsignedByte":
		return sparqlNumInteger, true
	case "decimal":
		return sparqlNumDecimal, true
	case "float":
		return sparqlNumFloat, true
	case "double":
		return sparqlNumDouble, true
	default:
		return 0, false
	}
}

func parseSPARQLFloat(lexical string) (float64, bool) {
	switch strings.TrimSpace(lexical) {
	case "INF", "+INF":
		return math.Inf(1), true
	case "-INF":
		return math.Inf(-1), true
	case "NaN":
		return math.NaN(), true
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(lexical), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// strictNumber recognises a literal typed with an XSD numeric datatype and a
// valid lexical form — what isNumeric and the spec mean by "numeric".
func strictNumber(term RDFTerm) (sparqlNumber, bool) {
	if term.Kind != RDFTermLiteral || term.Language != "" {
		return sparqlNumber{}, false
	}
	kind, ok := sparqlNumericKind(term.Datatype)
	if !ok {
		return sparqlNumber{}, false
	}
	if kind == sparqlNumInteger {
		if _, err := strconv.ParseInt(strings.TrimSpace(term.Value), 10, 64); err != nil {
			value, ok := parseSPARQLFloat(term.Value)
			if !ok || value != math.Trunc(value) || strings.ContainsAny(term.Value, ".eE") {
				return sparqlNumber{}, false
			}
			return sparqlNumber{kind: kind, value: value}, true
		}
	}
	value, ok := parseSPARQLFloat(term.Value)
	if !ok {
		return sparqlNumber{}, false
	}
	return sparqlNumber{kind: kind, value: value}, true
}

// lenientNumber also accepts an untyped literal whose text is a number. That
// is a deliberate deviation kept for compatibility: data written through the
// non-SPARQL APIs commonly stores numbers as plain strings, and arithmetic and
// comparison on them worked before the function library existed.
func lenientNumber(term RDFTerm) (sparqlNumber, bool) {
	if number, ok := strictNumber(term); ok {
		return number, true
	}
	if term.Kind != RDFTermLiteral || term.Language != "" || term.Datatype != "" {
		return sparqlNumber{}, false
	}
	text := strings.TrimSpace(term.Value)
	if _, err := strconv.ParseInt(text, 10, 64); err == nil {
		value, _ := strconv.ParseFloat(text, 64)
		return sparqlNumber{kind: sparqlNumInteger, value: value}, true
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return sparqlNumber{}, false
	}
	return sparqlNumber{kind: sparqlNumDecimal, value: value}, true
}

func (n sparqlNumber) term() RDFTerm {
	datatype := xsdIntegerIRI
	switch n.kind {
	case sparqlNumDecimal:
		datatype = xsdDecimalIRI
	case sparqlNumFloat:
		datatype = xsdFloatIRI
	case sparqlNumDouble:
		datatype = xsdDoubleIRI
	}
	var lexical string
	switch {
	case math.IsNaN(n.value):
		lexical = "NaN"
	case math.IsInf(n.value, 1):
		lexical = "INF"
	case math.IsInf(n.value, -1):
		lexical = "-INF"
	case n.kind == sparqlNumInteger:
		lexical = strconv.FormatFloat(math.Trunc(n.value), 'f', -1, 64)
	default:
		lexical = strconv.FormatFloat(n.value, 'f', -1, 64)
	}
	if lexical == "-0" {
		lexical = "0"
	}
	return NewTypedLiteral(lexical, datatype)
}

func sparqlNumericArg(name string, term RDFTerm) (sparqlNumber, error) {
	number, ok := lenientNumber(term)
	if !ok {
		return sparqlNumber{}, sparqlTypeErrorf("%s requires a numeric argument", name)
	}
	return number, nil
}

func sparqlArithmetic(op string, left, right RDFTerm) (RDFTerm, error) {
	a, ok := lenientNumber(left)
	if !ok {
		return RDFTerm{}, sparqlTypeErrorf("arithmetic operator %s requires numeric literals", op)
	}
	b, ok := lenientNumber(right)
	if !ok {
		return RDFTerm{}, sparqlTypeErrorf("arithmetic operator %s requires numeric literals", op)
	}
	kind := max(a.kind, b.kind)
	var result float64
	switch op {
	case "+":
		result = a.value + b.value
	case "-":
		result = a.value - b.value
	case "*":
		result = a.value * b.value
	case "/":
		// Integer division is decimal division (op:numeric-divide), and a
		// zero divisor is an error for exact types, IEEE for float/double.
		if kind == sparqlNumInteger {
			kind = sparqlNumDecimal
		}
		if b.value == 0 && kind == sparqlNumDecimal {
			return RDFTerm{}, sparqlTypeErrorf("division by zero")
		}
		result = a.value / b.value
	default:
		return RDFTerm{}, fmt.Errorf("unsupported arithmetic operator: %s", op)
	}
	return sparqlNumber{kind: kind, value: result}.term(), nil
}

// effectiveBooleanValue is SPARQL 1.1 §17.2.2. Anything outside booleans,
// numbers and plain strings is a type error, not "true because non-empty".
func effectiveBooleanValue(term RDFTerm) (bool, error) {
	if term.Kind != RDFTermLiteral {
		return false, sparqlTypeErrorf("no effective boolean value for a %s", term.Kind)
	}
	// An ill-typed boolean or number has no EBV: SPARQL 1.2 §17.2.2 makes it
	// an error where 1.1 made it false, so !!"z"^^xsd:boolean is unbound
	// rather than false. A FILTER drops the row either way.
	if term.Language == "" && term.Datatype == xsdBooleanIRI {
		switch term.Value {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
		return false, sparqlTypeErrorf("%q is not a valid xsd:boolean", term.Value)
	}
	if _, numeric := sparqlNumericKind(term.Datatype); numeric && term.Language == "" {
		number, ok := strictNumber(term)
		if !ok {
			return false, sparqlTypeErrorf("%q is not a valid %s", term.Value, term.Datatype)
		}
		return number.value != 0 && !math.IsNaN(number.value), nil
	}
	if isXSDStringLiteral(term) {
		return term.Value != "", nil
	}
	return false, sparqlTypeErrorf("no effective boolean value for this literal")
}

type sparqlDateTime struct {
	time  time.Time
	hasTZ bool
}

var sparqlDateTimeLayouts = []string{
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999",
}

func parseXSDDateTime(term RDFTerm) (sparqlDateTime, bool) {
	if term.Kind != RDFTermLiteral || term.Datatype != xsdDateTimeIRI {
		return sparqlDateTime{}, false
	}
	lexical := strings.TrimSpace(term.Value)
	for i, layout := range sparqlDateTimeLayouts {
		if parsed, err := time.Parse(layout, lexical); err == nil {
			return sparqlDateTime{time: parsed, hasTZ: i == 0}, true
		}
	}
	return sparqlDateTime{}, false
}

func requireDateTime(name string, term RDFTerm) (sparqlDateTime, error) {
	value, ok := parseXSDDateTime(term)
	if !ok {
		return sparqlDateTime{}, sparqlTypeErrorf("%s requires an xsd:dateTime", name)
	}
	return value, nil
}

// sparqlCompareOp implements = != < <= > >= with the operator mapping of
// SPARQL 1.1 §17.3: numbers by value, strings by code point, booleans and
// dateTimes by value, other terms by RDF term equality — and a type error for
// orderings the spec does not define.
func sparqlCompareOp(op string, left, right RDFTerm) (bool, error) {
	// RDF 1.2: = on two triple terms compares their parts by value, and a
	// triple term is never equal to anything that is not one (SPARQL 1.2
	// §17.4.1.7). Neither is ordered by < or >.
	if left.Kind == RDFTermTriple || right.Kind == RDFTermTriple {
		if op != "=" && op != "!=" {
			return false, sparqlTypeErrorf("operator %s is not defined for triple terms", op)
		}
		equal := false
		if left.Kind == RDFTermTriple && right.Kind == RDFTermTriple {
			var err error
			if equal, err = sparqlTripleTermsEqual(left, right); err != nil {
				return false, err
			}
		}
		if op == "=" {
			return equal, nil
		}
		return !equal, nil
	}
	cmp, comparable, err := sparqlValueCompare(left, right)
	if err != nil {
		return false, err
	}
	if !comparable {
		if op != "=" && op != "!=" {
			return false, sparqlTypeErrorf("operator %s is not defined for these terms", op)
		}
		equal := termsEqual(left, right)
		if !equal && left.Kind == RDFTermLiteral && right.Kind == RDFTermLiteral {
			return false, sparqlTypeErrorf("cannot compare literals of different types")
		}
		if op == "=" {
			return equal, nil
		}
		return !equal, nil
	}
	switch op {
	case "=":
		return cmp == 0, nil
	case "!=":
		return cmp != 0, nil
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case ">=":
		return cmp >= 0, nil
	default:
		return false, fmt.Errorf("unsupported comparison operator: %s", op)
	}
}

// sparqlValueCompare orders two terms by value when the spec gives them a
// value ordering, and reports comparable=false otherwise.
func sparqlValueCompare(left, right RDFTerm) (int, bool, error) {
	if left.Kind != RDFTermLiteral || right.Kind != RDFTermLiteral {
		return 0, false, nil
	}
	if a, ok := strictNumber(left); ok {
		if b, ok := lenientNumber(right); ok {
			return sparqlCompareNumbers(a.value, b.value), true, nil
		}
	}
	if b, ok := strictNumber(right); ok {
		if a, ok := lenientNumber(left); ok {
			return sparqlCompareNumbers(a.value, b.value), true, nil
		}
	}
	if isXSDStringLiteral(left) && isXSDStringLiteral(right) {
		return strings.Compare(left.Value, right.Value), true, nil
	}
	if left.Datatype == xsdBooleanIRI && right.Datatype == xsdBooleanIRI {
		a, _ := effectiveBooleanValue(left)
		b, _ := effectiveBooleanValue(right)
		switch {
		case a == b:
			return 0, true, nil
		case !a:
			return -1, true, nil
		default:
			return 1, true, nil
		}
	}
	if a, ok := parseXSDDateTime(left); ok {
		if b, ok := parseXSDDateTime(right); ok {
			return a.time.Compare(b.time), true, nil
		}
	}
	return 0, false, nil
}

func sparqlCompareNumbers(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// sparqlOrderCompare is the total order ORDER BY, MIN and MAX need (§15.1):
// unbound < blank nodes < IRIs < literals. Literals are first split into
// classes — numbers, booleans, dateTimes, plain strings, language-tagged
// strings, everything else — and ordered by value only within a class.
// Comparing across classes by value is what makes a comparator
// non-transitive (2 < "10" numerically, "10" < "1a" lexically, "1a" < "2"
// lexically), and sort.SliceStable over a non-transitive order returns
// garbage. An untyped "10" is a string here, as it is for the < operator.
func sparqlOrderCompare(left RDFTerm, leftOK bool, right RDFTerm, rightOK bool) int {
	switch {
	case !leftOK && !rightOK:
		return 0
	case !leftOK:
		return -1
	case !rightOK:
		return 1
	}
	if cmp := compareInt(sparqlOrderClass(left), sparqlOrderClass(right)); cmp != 0 {
		return cmp
	}
	if left.Kind == RDFTermTriple {
		return sparqlCompareTripleTermsForOrder(left, right)
	}
	if left.Kind == RDFTermLiteral {
		if cmp, comparable, _ := sparqlValueCompare(left, right); comparable && cmp != 0 {
			return cmp
		}
	}
	for _, pair := range [][2]string{{left.Value, right.Value}, {left.Language, right.Language}, {left.Datatype, right.Datatype}} {
		if cmp := strings.Compare(pair[0], pair[1]); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func sparqlOrderClass(term RDFTerm) int {
	switch term.Kind {
	case RDFTermBlankNode:
		return 0
	case RDFTermIRI:
		return 1
	case RDFTermLiteral:
	case RDFTermTriple:
		// SPARQL 1.2 §15.1: triple terms sort after every literal.
		return 8
	default:
		return 9
	}
	switch {
	case isLangLiteral(term):
		return 6
	case isXSDStringLiteral(term):
		return 5
	case term.Datatype == xsdBooleanIRI:
		return 3
	case term.Datatype == xsdDateTimeIRI:
		if _, ok := parseXSDDateTime(term); ok {
			return 4
		}
		return 7
	}
	if _, ok := strictNumber(term); ok {
		return 2
	}
	return 7
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// Function library
// ---------------------------------------------------------------------------

type sparqlFunction struct {
	minArgs int
	maxArgs int // -1 for variadic
	eval    func(rt *sparqlRuntime, args []RDFTerm) (RDFTerm, error)
}

// sparqlFunctions is the SPARQL 1.1 §17.4 built-in library, keyed by the
// upper-cased name. BOUND, IF, COALESCE, EXISTS and IN are not here: they do
// not evaluate all their arguments eagerly, so the parser builds dedicated
// nodes for them.
var sparqlFunctions map[string]*sparqlFunction

func init() {
	sparqlFunctions = map[string]*sparqlFunction{
		// term tests and accessors
		"ISIRI":     {1, 1, sparqlFnKindTest(RDFTermIRI)},
		"ISURI":     {1, 1, sparqlFnKindTest(RDFTermIRI)},
		"ISBLANK":   {1, 1, sparqlFnKindTest(RDFTermBlankNode)},
		"ISLITERAL": {1, 1, sparqlFnKindTest(RDFTermLiteral)},
		"ISNUMERIC": {1, 1, func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
			_, ok := strictNumber(a[0])
			return booleanTerm(ok), nil
		}},
		"STR":         {1, 1, sparqlFnStr},
		"LANG":        {1, 1, sparqlFnLang},
		"DATATYPE":    {1, 1, sparqlFnDatatype},
		"IRI":         {1, 1, sparqlFnIRI},
		"URI":         {1, 1, sparqlFnIRI},
		"BNODE":       {0, 1, sparqlFnBNode},
		"LANGMATCHES": {2, 2, sparqlFnLangMatches},
		"SAMETERM":    {2, 2, func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) { return booleanTerm(termsEqual(a[0], a[1])), nil }},
		"STRDT":       {2, 2, sparqlFnStrDT},
		"STRLANG":     {2, 2, sparqlFnStrLang},
		"UUID": {0, 0, func(_ *sparqlRuntime, _ []RDFTerm) (RDFTerm, error) {
			return NewIRI("urn:uuid:" + newUUIDv4()), nil
		}},
		"STRUUID": {0, 0, func(_ *sparqlRuntime, _ []RDFTerm) (RDFTerm, error) { return NewLiteral(newUUIDv4()), nil }},

		// strings
		"STRLEN":         {1, 1, sparqlFnStrLen},
		"SUBSTR":         {2, 3, sparqlFnSubstr},
		"UCASE":          {1, 1, sparqlFnCase(strings.ToUpper, "UCASE")},
		"LCASE":          {1, 1, sparqlFnCase(strings.ToLower, "LCASE")},
		"STRSTARTS":      {2, 2, sparqlFnStringTest("STRSTARTS", strings.HasPrefix)},
		"STRENDS":        {2, 2, sparqlFnStringTest("STRENDS", strings.HasSuffix)},
		"CONTAINS":       {2, 2, sparqlFnStringTest("CONTAINS", strings.Contains)},
		"STRBEFORE":      {2, 2, sparqlFnStrBefore},
		"STRAFTER":       {2, 2, sparqlFnStrAfter},
		"CONCAT":         {0, -1, sparqlFnConcat},
		"REPLACE":        {3, 4, sparqlFnReplace},
		"REGEX":          {2, 3, sparqlFnRegex},
		"ENCODE_FOR_URI": {1, 1, sparqlFnEncodeForURI},

		// numerics
		"ABS":   {1, 1, sparqlFnNumeric("ABS", math.Abs)},
		"CEIL":  {1, 1, sparqlFnNumeric("CEIL", math.Ceil)},
		"FLOOR": {1, 1, sparqlFnNumeric("FLOOR", math.Floor)},
		// fn:round rounds halves towards positive infinity: ROUND(-2.5) is -2.
		"ROUND": {1, 1, sparqlFnNumeric("ROUND", func(v float64) float64 { return math.Floor(v + 0.5) })},
		"RAND": {0, 0, func(_ *sparqlRuntime, _ []RDFTerm) (RDFTerm, error) {
			return NewTypedLiteral(strconv.FormatFloat(mathrand.Float64(), 'f', -1, 64), xsdDoubleIRI), nil
		}},

		// dates
		"NOW":      {0, 0, func(rt *sparqlRuntime, _ []RDFTerm) (RDFTerm, error) { return rt.now, nil }},
		"YEAR":     {1, 1, sparqlFnDatePart("YEAR", func(t time.Time) int { return t.Year() })},
		"MONTH":    {1, 1, sparqlFnDatePart("MONTH", func(t time.Time) int { return int(t.Month()) })},
		"DAY":      {1, 1, sparqlFnDatePart("DAY", func(t time.Time) int { return t.Day() })},
		"HOURS":    {1, 1, sparqlFnDatePart("HOURS", func(t time.Time) int { return t.Hour() })},
		"MINUTES":  {1, 1, sparqlFnDatePart("MINUTES", func(t time.Time) int { return t.Minute() })},
		"SECONDS":  {1, 1, sparqlFnSeconds},
		"TIMEZONE": {1, 1, sparqlFnTimezone},
		"TZ":       {1, 1, sparqlFnTZ},

		// hashes
		"MD5":    {1, 1, sparqlFnHash("MD5", md5.New)},
		"SHA1":   {1, 1, sparqlFnHash("SHA1", sha1.New)},
		"SHA256": {1, 1, sparqlFnHash("SHA256", sha256.New)},
		"SHA384": {1, 1, sparqlFnHash("SHA384", sha512.New384)},
		"SHA512": {1, 1, sparqlFnHash("SHA512", sha512.New)},
	}
	registerSPARQL12Functions()
}

func sparqlFnKindTest(kind string) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		return booleanTerm(a[0].Kind == kind), nil
	}
}

func sparqlFnStr(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	switch a[0].Kind {
	case RDFTermIRI, RDFTermLiteral:
		return NewLiteral(a[0].Value), nil
	default:
		return RDFTerm{}, sparqlTypeErrorf("STR is not defined for a blank node")
	}
}

// sparqlFnLang returns the language tag alone: in SPARQL 1.2 a base
// direction is LANGDIR's answer, not part of LANG's.
func sparqlFnLang(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if a[0].Kind != RDFTermLiteral {
		return RDFTerm{}, sparqlTypeErrorf("LANG requires a literal")
	}
	return NewLiteral(a[0].LanguageTag()), nil
}

func sparqlFnDatatype(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	switch {
	case a[0].Kind != RDFTermLiteral:
		return RDFTerm{}, sparqlTypeErrorf("DATATYPE requires a literal")
	case a[0].BaseDirection() != "":
		return NewIRI(rdf12DirLangStringIRI), nil
	case a[0].Language != "":
		return NewIRI(rdfLangStringIRI), nil
	case a[0].Datatype == "":
		return NewIRI(xsdStringIRI), nil
	default:
		return NewIRI(a[0].Datatype), nil
	}
}

// sparqlFnIRI has no BASE to resolve against, so a relative string becomes a
// relative IRI as written.
func sparqlFnIRI(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	switch {
	case a[0].Kind == RDFTermIRI:
		return a[0], nil
	case isXSDStringLiteral(a[0]):
		return NewIRI(a[0].Value), nil
	default:
		return RDFTerm{}, sparqlTypeErrorf("IRI requires an IRI or a simple literal")
	}
}

func sparqlFnBNode(rt *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if len(a) == 0 {
		return rt.freshBlankNode(), nil
	}
	if err := requireSimpleString("BNODE", a[0]); err != nil {
		return RDFTerm{}, err
	}
	return rt.labelledBlankNode(a[0].Value), nil
}

func sparqlFnLangMatches(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireSimpleString("LANGMATCHES", a[0]); err != nil {
		return RDFTerm{}, err
	}
	if err := requireSimpleString("LANGMATCHES", a[1]); err != nil {
		return RDFTerm{}, err
	}
	tag, languageRange := strings.ToLower(a[0].Value), strings.ToLower(a[1].Value)
	if languageRange == "*" {
		return booleanTerm(tag != ""), nil
	}
	return booleanTerm(tag == languageRange || strings.HasPrefix(tag, languageRange+"-")), nil
}

func sparqlFnStrDT(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireSimpleString("STRDT", a[0]); err != nil {
		return RDFTerm{}, err
	}
	if a[1].Kind != RDFTermIRI {
		return RDFTerm{}, sparqlTypeErrorf("STRDT requires a datatype IRI")
	}
	return NewTypedLiteral(a[0].Value, a[1].Value), nil
}

func sparqlFnStrLang(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireSimpleString("STRLANG", a[0]); err != nil {
		return RDFTerm{}, err
	}
	if err := requireSimpleString("STRLANG", a[1]); err != nil {
		return RDFTerm{}, err
	}
	if strings.TrimSpace(a[1].Value) == "" {
		return RDFTerm{}, sparqlTypeErrorf("STRLANG requires a non-empty language tag")
	}
	return NewLangLiteral(a[0].Value, a[1].Value), nil
}

func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func sparqlFnStrLen(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireStringLiteral("STRLEN", a[0]); err != nil {
		return RDFTerm{}, err
	}
	return NewTypedLiteral(strconv.Itoa(utf8.RuneCountInString(a[0].Value)), xsdIntegerIRI), nil
}

// sparqlFnSubstr is fn:substring: 1-based code-point positions, with start
// and length rounded, keeping characters at positions p where
// round(start) <= p < round(start)+round(length).
func sparqlFnSubstr(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireStringLiteral("SUBSTR", a[0]); err != nil {
		return RDFTerm{}, err
	}
	start, err := sparqlNumericArg("SUBSTR", a[1])
	if err != nil {
		return RDFTerm{}, err
	}
	first := math.Floor(start.value + 0.5)
	last := math.Inf(1)
	if len(a) == 3 {
		length, err := sparqlNumericArg("SUBSTR", a[2])
		if err != nil {
			return RDFTerm{}, err
		}
		last = first + math.Floor(length.value+0.5)
	}
	if math.IsNaN(first) || math.IsNaN(last) {
		return stringLike("", a[0]), nil
	}
	var out strings.Builder
	position := 0.0
	for _, r := range a[0].Value {
		position++
		if position >= first && position < last {
			out.WriteRune(r)
		}
	}
	return stringLike(out.String(), a[0]), nil
}

func sparqlFnCase(transform func(string) string, name string) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		if err := requireStringLiteral(name, a[0]); err != nil {
			return RDFTerm{}, err
		}
		return stringLike(transform(a[0].Value), a[0]), nil
	}
}

func sparqlFnStringTest(name string, test func(string, string) bool) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		if err := argumentsCompatible(name, a[0], a[1]); err != nil {
			return RDFTerm{}, err
		}
		return booleanTerm(test(a[0].Value, a[1].Value)), nil
	}
}

func sparqlFnStrBefore(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := argumentsCompatible("STRBEFORE", a[0], a[1]); err != nil {
		return RDFTerm{}, err
	}
	index := strings.Index(a[0].Value, a[1].Value)
	if index < 0 {
		return NewLiteral(""), nil
	}
	return stringLike(a[0].Value[:index], a[0]), nil
}

func sparqlFnStrAfter(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := argumentsCompatible("STRAFTER", a[0], a[1]); err != nil {
		return RDFTerm{}, err
	}
	index := strings.Index(a[0].Value, a[1].Value)
	if index < 0 {
		return NewLiteral(""), nil
	}
	return stringLike(a[0].Value[index+len(a[1].Value):], a[0]), nil
}

// sparqlFnConcat keeps a shared language tag or a shared xsd:string datatype,
// and otherwise produces a simple literal.
func sparqlFnConcat(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	var out strings.Builder
	shared := len(a) > 0
	for _, arg := range a {
		if err := requireStringLiteral("CONCAT", arg); err != nil {
			return RDFTerm{}, err
		}
		if arg.Language != a[0].Language || arg.Datatype != a[0].Datatype {
			shared = false
		}
		out.WriteString(arg.Value)
	}
	if !shared {
		return NewLiteral(out.String()), nil
	}
	return stringLike(out.String(), a[0]), nil
}

func sparqlFnRegex(rt *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireStringLiteral("REGEX", a[0]); err != nil {
		return RDFTerm{}, err
	}
	re, err := regexArgs(rt, "REGEX", a[1:])
	if err != nil {
		return RDFTerm{}, err
	}
	return booleanTerm(re.MatchString(a[0].Value)), nil
}

func regexArgs(rt *sparqlRuntime, name string, a []RDFTerm) (*regexp.Regexp, error) {
	if err := requireSimpleString(name, a[0]); err != nil {
		return nil, err
	}
	flags := ""
	if len(a) > 1 {
		if err := requireSimpleString(name, a[1]); err != nil {
			return nil, err
		}
		flags = a[1].Value
	}
	return rt.compileRegex(a[0].Value, flags)
}

// sparqlFnReplace is fn:replace: $N in the replacement is a group reference,
// \$ and \\ are literal, and a pattern that matches the empty string is an
// error (it would insert the replacement between every character).
func sparqlFnReplace(rt *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireStringLiteral("REPLACE", a[0]); err != nil {
		return RDFTerm{}, err
	}
	if err := requireSimpleString("REPLACE", a[2]); err != nil {
		return RDFTerm{}, err
	}
	regexArgsList := []RDFTerm{a[1]}
	if len(a) == 4 {
		regexArgsList = append(regexArgsList, a[3])
	}
	re, err := regexArgs(rt, "REPLACE", regexArgsList)
	if err != nil {
		return RDFTerm{}, err
	}
	if re.MatchString("") {
		return RDFTerm{}, sparqlTypeErrorf("REPLACE pattern matches the empty string")
	}
	template, err := xpathReplacementTemplate(a[2].Value, re.NumSubexp())
	if err != nil {
		return RDFTerm{}, err
	}
	return stringLike(re.ReplaceAllString(a[0].Value, template), a[0]), nil
}

// xpathReplacementTemplate translates an XPath replacement string into Go's
// Expand syntax, where "$1a" would otherwise name a group called "1a".
func xpathReplacementTemplate(replacement string, groups int) (string, error) {
	var out strings.Builder
	for i := 0; i < len(replacement); i++ {
		switch ch := replacement[i]; ch {
		case '\\':
			if i+1 >= len(replacement) || (replacement[i+1] != '\\' && replacement[i+1] != '$') {
				return "", sparqlTypeErrorf("invalid escape in REPLACE replacement")
			}
			i++
			if replacement[i] == '$' {
				out.WriteString("$$")
			} else {
				out.WriteByte('\\')
			}
		case '$':
			j := i + 1
			for j < len(replacement) && replacement[j] >= '0' && replacement[j] <= '9' {
				j++
			}
			if j == i+1 {
				return "", sparqlTypeErrorf("invalid $ in REPLACE replacement")
			}
			// XPath takes the longest run of digits that still names a group.
			end := j
			for end > i+2 {
				if n, _ := strconv.Atoi(replacement[i+1 : end]); n <= groups {
					break
				}
				end--
			}
			n, _ := strconv.Atoi(replacement[i+1 : end])
			if n <= groups {
				out.WriteString("${" + strconv.Itoa(n) + "}")
			}
			out.WriteString(replacement[end:j])
			i = j - 1
		default:
			out.WriteByte(ch)
		}
	}
	return out.String(), nil
}

func sparqlFnEncodeForURI(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if err := requireStringLiteral("ENCODE_FOR_URI", a[0]); err != nil {
		return RDFTerm{}, err
	}
	const hexDigits = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(a[0].Value); i++ {
		c := a[0].Value[i]
		if ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hexDigits[c>>4])
		out.WriteByte(hexDigits[c&0x0f])
	}
	return NewLiteral(out.String()), nil
}

func sparqlFnNumeric(name string, op func(float64) float64) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		number, err := sparqlNumericArg(name, a[0])
		if err != nil {
			return RDFTerm{}, err
		}
		number.value = op(number.value)
		return number.term(), nil
	}
}

func sparqlFnDatePart(name string, part func(time.Time) int) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		value, err := requireDateTime(name, a[0])
		if err != nil {
			return RDFTerm{}, err
		}
		return NewTypedLiteral(strconv.Itoa(part(value.time)), xsdIntegerIRI), nil
	}
}

func sparqlFnSeconds(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	value, err := requireDateTime("SECONDS", a[0])
	if err != nil {
		return RDFTerm{}, err
	}
	seconds := float64(value.time.Second()) + float64(value.time.Nanosecond())/1e9
	return sparqlNumber{kind: sparqlNumDecimal, value: seconds}.term(), nil
}

func sparqlFnTimezone(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	value, err := requireDateTime("TIMEZONE", a[0])
	if err != nil {
		return RDFTerm{}, err
	}
	if !value.hasTZ {
		return RDFTerm{}, sparqlTypeErrorf("TIMEZONE of a dateTime without a timezone")
	}
	_, offset := value.time.Zone()
	if offset == 0 {
		return NewTypedLiteral("PT0S", xsdDayTimeDuration), nil
	}
	sign := ""
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	duration := sign + "PT"
	if hours := offset / 3600; hours > 0 {
		duration += strconv.Itoa(hours) + "H"
	}
	if minutes := (offset % 3600) / 60; minutes > 0 {
		duration += strconv.Itoa(minutes) + "M"
	}
	return NewTypedLiteral(duration, xsdDayTimeDuration), nil
}

func sparqlFnTZ(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	value, err := requireDateTime("TZ", a[0])
	if err != nil {
		return RDFTerm{}, err
	}
	if !value.hasTZ {
		return NewLiteral(""), nil
	}
	if _, offset := value.time.Zone(); offset == 0 {
		return NewLiteral("Z"), nil
	}
	return NewLiteral(value.time.Format("-07:00")), nil
}

func sparqlFnHash(name string, newHash func() hash.Hash) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		if err := requireSimpleString(name, a[0]); err != nil {
			return RDFTerm{}, err
		}
		h := newHash()
		h.Write([]byte(a[0].Value))
		return NewLiteral(hex.EncodeToString(h.Sum(nil))), nil
	}
}
