package cypher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Values in this package are plain Go: nil, bool, int64, float64, string,
// []any, map[string]any, *Node, *Rel and *Path. Keeping them plain is what
// lets a result row go straight to encoding/json and to a test's
// reflect.DeepEqual without an adapter in between.

// Node is a matched graph node.
//
// Labels is the node's single node_type as a one-element list, or empty when
// the node has none: the property graph has one type per node, and labels(n)
// returning a list is what keeps openCypher queries written against
// multi-label graphs meaningful here.
type Node struct {
	ID         string         `json:"id"`
	Labels     []string       `json:"labels"`
	Content    string         `json:"content,omitempty"`
	Properties map[string]any `json:"properties"`

	// raw is the properties column, decoded on first use. A scan that only
	// counts labels passes over thousands of nodes whose JSON nobody reads.
	raw     string
	decoded bool
}

// Rel is a matched relationship (an edge).
type Rel struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	StartID    string         `json:"start"`
	EndID      string         `json:"end"`
	Weight     float64        `json:"weight"`
	Properties map[string]any `json:"properties"`

	raw     string
	decoded bool
}

func (n *Node) props() (map[string]any, error) {
	if !n.decoded {
		m, err := decodeProps(n.raw)
		if err != nil {
			return nil, runtimeErr("node %q has malformed properties: %v", n.ID, err)
		}
		n.Properties, n.decoded, n.raw = m, true, ""
	}
	return n.Properties, nil
}

func (r *Rel) props() (map[string]any, error) {
	if !r.decoded {
		m, err := decodeProps(r.raw)
		if err != nil {
			return nil, runtimeErr("edge %q has malformed properties: %v", r.ID, err)
		}
		r.Properties, r.decoded, r.raw = m, true, ""
	}
	return r.Properties, nil
}

// materialize decodes the properties of every node and relationship inside
// v, so a result handed to a caller is complete.
func materialize(v any) error {
	switch x := v.(type) {
	case *Node:
		_, err := x.props()
		return err
	case *Rel:
		_, err := x.props()
		return err
	case *Path:
		for _, n := range x.Nodes {
			if err := materialize(n); err != nil {
				return err
			}
		}
		for _, r := range x.Rels {
			if err := materialize(r); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := materialize(e); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, e := range x {
			if err := materialize(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// Path is a named path: nodes and the relationships between them, in
// traversal order (len(Rels) == len(Nodes)-1).
type Path struct {
	Nodes []*Node `json:"nodes"`
	Rels  []*Rel  `json:"relationships"`
}

func (n *Node) label() string {
	if len(n.Labels) == 0 {
		return ""
	}
	return n.Labels[0]
}

// decodeProps reads a properties column. Numbers keep their kind: a JSON
// integer becomes int64 and anything with a fraction or exponent float64, so
// `n.port = 8080` compares as integers and is returned as one.
func decodeProps(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	m, ok := normalizeJSON(v).(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return m, nil
}

func normalizeJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return i
		}
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return string(x)
		}
		return f
	case float64:
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeJSON(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalizeJSON(e)
		}
		return out
	}
	return v
}

// NormalizeParam turns a caller's parameter value into a value this package
// evaluates. JSON-decoded numbers arrive as float64; an integral one becomes
// int64, so `LIMIT $n` and `n.port = $p` behave the way the caller meant.
func NormalizeParam(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string, int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case float32:
		return NormalizeParam(float64(x))
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x), nil
		}
		return x, nil
	case json.Number:
		return normalizeJSON(x), nil
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i := range x {
			e, err := NormalizeParam(x[i])
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			n, err := NormalizeParam(e)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported parameter type %T", v)
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int64:
		return "integer"
	case float64:
		return "float"
	case string:
		return "string"
	case []any:
		return "list"
	case map[string]any:
		return "map"
	case *Node:
		return "node"
	case *Rel:
		return "relationship"
	case *Path:
		return "path"
	}
	return fmt.Sprintf("%T", v)
}

func isNumber(v any) bool {
	switch v.(type) {
	case int64, float64:
		return true
	}
	return false
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return math.NaN()
}

// tri is a ternary truth value: openCypher logic over null.
type tri int

const (
	triFalse tri = iota
	triTrue
	triNull
)

func triOf(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

func (t tri) value() any {
	switch t {
	case triTrue:
		return true
	case triFalse:
		return false
	}
	return nil
}

// equals is openCypher `=`: null when either side is null (or a list holds a
// null that decides it), false across types, numeric across int and float.
func equals(a, b any) tri {
	if a == nil || b == nil {
		return triNull
	}
	switch x := a.(type) {
	case int64:
		switch y := b.(type) {
		case int64:
			return triOf(x == y)
		case float64:
			return triOf(float64(x) == y)
		}
		return triFalse
	case float64:
		switch y := b.(type) {
		case int64:
			return triOf(x == float64(y))
		case float64:
			return triOf(x == y)
		}
		return triFalse
	case string:
		y, ok := b.(string)
		return triOf(ok && x == y)
	case bool:
		y, ok := b.(bool)
		return triOf(ok && x == y)
	case []any:
		y, ok := b.([]any)
		if !ok {
			return triFalse
		}
		if len(x) != len(y) {
			return triFalse
		}
		res := triTrue
		for i := range x {
			switch equals(x[i], y[i]) {
			case triFalse:
				return triFalse
			case triNull:
				res = triNull
			}
		}
		return res
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return triFalse
		}
		res := triTrue
		for k, xv := range x {
			yv, ok := y[k]
			if !ok {
				return triFalse
			}
			switch equals(xv, yv) {
			case triFalse:
				return triFalse
			case triNull:
				res = triNull
			}
		}
		return res
	case *Node:
		y, ok := b.(*Node)
		return triOf(ok && x.ID == y.ID)
	case *Rel:
		y, ok := b.(*Rel)
		return triOf(ok && x.ID == y.ID)
	case *Path:
		y, ok := b.(*Path)
		if !ok || len(x.Rels) != len(y.Rels) || len(x.Nodes) != len(y.Nodes) {
			return triFalse
		}
		for i := range x.Nodes {
			if x.Nodes[i].ID != y.Nodes[i].ID {
				return triFalse
			}
		}
		for i := range x.Rels {
			if x.Rels[i].ID != y.Rels[i].ID {
				return triFalse
			}
		}
		return triTrue
	}
	return triFalse
}

// compare is openCypher comparability for < > <= >=: ok is false (the
// comparison is null) when the two are not of comparable types.
func compare(a, b any) (int, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	switch x := a.(type) {
	case int64:
		switch y := b.(type) {
		case int64:
			return cmpInt(x, y), true
		case float64:
			if math.IsNaN(y) {
				return 0, false
			}
			return cmpFloat(float64(x), y), true
		}
	case float64:
		if math.IsNaN(x) {
			return 0, false
		}
		switch y := b.(type) {
		case int64:
			return cmpFloat(x, float64(y)), true
		case float64:
			if math.IsNaN(y) {
				return 0, false
			}
			return cmpFloat(x, y), true
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y), true
		}
	case bool:
		if y, ok := b.(bool); ok {
			return cmpBool(x, y), true
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			return 0, false
		}
		for i := 0; i < len(x) && i < len(y); i++ {
			if equals(x[i], y[i]) == triTrue {
				continue
			}
			c, ok := compare(x[i], y[i])
			if !ok {
				return 0, false
			}
			if c != 0 {
				return c, true
			}
		}
		return cmpInt(int64(len(x)), int64(len(y))), true
	}
	return 0, false
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

// orderRank is the openCypher global sort order between types, ascending:
// map, node, relationship, list, path, string, boolean, number, null.
func orderRank(v any) int {
	switch x := v.(type) {
	case map[string]any:
		return 0
	case *Node:
		return 1
	case *Rel:
		return 2
	case []any:
		return 3
	case *Path:
		return 4
	case string:
		return 5
	case bool:
		return 6
	case int64:
		return 7
	case float64:
		if math.IsNaN(x) {
			return 8
		}
		return 7
	case nil:
		return 9
	}
	return 10
}

// orderCompare is the total order ORDER BY, min and max use. Unlike compare
// it never refuses: values of different types sort by orderRank.
func orderCompare(a, b any) int {
	ra, rb := orderRank(a), orderRank(b)
	if ra != rb {
		return cmpInt(int64(ra), int64(rb))
	}
	switch x := a.(type) {
	case nil:
		return 0
	case int64, float64:
		if xf, ok := x.(float64); ok && math.IsNaN(xf) {
			return 0
		}
		c, _ := compare(a, b)
		if c == 0 {
			// 1 and 1.0 are equal; keep the order total and deterministic.
			_, ai := a.(int64)
			_, bi := b.(int64)
			if ai != bi {
				if ai {
					return -1
				}
				return 1
			}
		}
		return c
	case string:
		return strings.Compare(x, b.(string))
	case bool:
		return cmpBool(x, b.(bool))
	case []any:
		y := b.([]any)
		for i := 0; i < len(x) && i < len(y); i++ {
			if c := orderCompare(x[i], y[i]); c != 0 {
				return c
			}
		}
		return cmpInt(int64(len(x)), int64(len(y)))
	case map[string]any:
		y := b.(map[string]any)
		kx, ky := sortedKeys(x), sortedKeys(y)
		for i := 0; i < len(kx) && i < len(ky); i++ {
			if c := strings.Compare(kx[i], ky[i]); c != 0 {
				return c
			}
			if c := orderCompare(x[kx[i]], y[ky[i]]); c != 0 {
				return c
			}
		}
		return cmpInt(int64(len(kx)), int64(len(ky)))
	case *Node:
		return strings.Compare(x.ID, b.(*Node).ID)
	case *Rel:
		return strings.Compare(x.ID, b.(*Rel).ID)
	case *Path:
		y := b.(*Path)
		for i := 0; i < len(x.Nodes) && i < len(y.Nodes); i++ {
			if c := strings.Compare(x.Nodes[i].ID, y.Nodes[i].ID); c != 0 {
				return c
			}
		}
		for i := 0; i < len(x.Rels) && i < len(y.Rels); i++ {
			if c := strings.Compare(x.Rels[i].ID, y.Rels[i].ID); c != 0 {
				return c
			}
		}
		return cmpInt(int64(len(x.Rels)), int64(len(y.Rels)))
	}
	return 0
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// groupKey is a byte string equal for exactly the values DISTINCT and
// grouping treat as the same: equivalence, not equality — null groups with
// null, and 1 groups with 1.0.
func groupKey(vals ...any) string {
	var b bytes.Buffer
	for _, v := range vals {
		writeKey(&b, v)
		b.WriteByte(0)
	}
	return b.String()
}

func writeKey(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("N")
	case bool:
		if x {
			b.WriteString("T")
		} else {
			b.WriteString("F")
		}
	case int64:
		b.WriteString("#")
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 64))
		if float64(x) != float64(int64(float64(x))) || int64(float64(x)) != x {
			b.WriteString("i")
			b.WriteString(strconv.FormatInt(x, 10))
		}
	case float64:
		b.WriteString("#")
		if math.IsNaN(x) {
			b.WriteString("NaN")
		} else {
			b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
		}
	case string:
		b.WriteString("S")
		b.WriteString(strconv.Itoa(len(x)))
		b.WriteString(":")
		b.WriteString(x)
	case []any:
		b.WriteString("[")
		for _, e := range x {
			writeKey(b, e)
			b.WriteString(",")
		}
		b.WriteString("]")
	case map[string]any:
		b.WriteString("{")
		for _, k := range sortedKeys(x) {
			b.WriteString(strconv.Itoa(len(k)))
			b.WriteString(":")
			b.WriteString(k)
			writeKey(b, x[k])
			b.WriteString(",")
		}
		b.WriteString("}")
	case *Node:
		b.WriteString("n")
		b.WriteString(strconv.Itoa(len(x.ID)))
		b.WriteString(":")
		b.WriteString(x.ID)
	case *Rel:
		b.WriteString("r")
		b.WriteString(strconv.Itoa(len(x.ID)))
		b.WriteString(":")
		b.WriteString(x.ID)
	case *Path:
		b.WriteString("p(")
		for _, n := range x.Nodes {
			writeKey(b, n)
		}
		for _, r := range x.Rels {
			writeKey(b, r)
		}
		b.WriteString(")")
	default:
		fmt.Fprintf(b, "?%v", x)
	}
}
