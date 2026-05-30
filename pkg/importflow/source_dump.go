// pkg/importflow/source_dump.go
package importflow

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Dialect selects dump-specific parsing rules.
type Dialect string

const (
	DialectAuto     Dialect = "auto"
	DialectMySQL    Dialect = "mysql"
	DialectPostgres Dialect = "postgres"
)

// DumpOptions configures a SQLDumpSource.
type DumpOptions struct {
	Dialect    Dialect // default DialectAuto
	SampleSize int     // sample rows per table in Schemas(); default 5
}

// SQLDumpSource parses a common subset of MySQL/PG dumps: CREATE TABLE (for
// column order), INSERT INTO ... VALUES (...), and PG COPY ... \. blocks.
// Unrecognized statements are recorded in Unparsed() instead of being dropped.
type SQLDumpSource struct {
	dialect    Dialect
	sampleSize int
	columns    map[string][]Column // table -> declared columns (from CREATE TABLE)
	order      []string            // table insertion order for stable Schemas()
	records    []Record
	unparsed   []string
	copyTable  string   // current COPY block target table
	copyCols   []string // current COPY block columns
}

// NewSQLDumpSource parses the entire dump eagerly.
func NewSQLDumpSource(r io.Reader, opts DumpOptions) (*SQLDumpSource, error) {
	if opts.Dialect == "" {
		opts.Dialect = DialectAuto
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read dump: %w", err)
	}
	s := &SQLDumpSource{
		dialect:    opts.Dialect,
		sampleSize: opts.SampleSize,
		columns:    map[string][]Column{},
	}
	if err := s.parse(string(data)); err != nil {
		return nil, err
	}
	return s, nil
}

// Unparsed returns statements the parser could not handle.
func (s *SQLDumpSource) Unparsed() []string { return s.unparsed }

func (s *SQLDumpSource) parse(dump string) error {
	remainder := s.extractCopyBlocks(dump)
	for _, stmt := range splitStatements(remainder) {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"):
			s.parseCreateTable(trimmed)
		case strings.HasPrefix(upper, "INSERT INTO"):
			if err := s.parseInsert(trimmed); err != nil {
				s.unparsed = append(s.unparsed, trimmed)
			}
		default:
			s.unparsed = append(s.unparsed, trimmed)
		}
	}
	return nil
}

// extractCopyBlocks consumes "COPY <t> (cols) FROM stdin; <tab-rows> \." blocks
// and returns the dump with those blocks removed (so the leftover is safe to
// split on ';'). CREATE TABLE statements may appear before the COPY block, so
// scan the remainder for them first.
func (s *SQLDumpSource) extractCopyBlocks(dump string) string {
	lines := strings.Split(dump, "\n")
	var kept []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		up := strings.ToUpper(strings.TrimSpace(line))
		if strings.HasPrefix(up, "COPY ") && strings.Contains(up, "FROM STDIN") {
			s.parseCopyHeader(line)
			i++
			for i < len(lines) {
				if strings.TrimSpace(lines[i]) == `\.` {
					i++
					break
				}
				s.parseCopyRow(lines[i])
				i++
			}
			continue
		}
		kept = append(kept, line)
		i++
	}
	return strings.Join(kept, "\n")
}

// copyState tracks the table/columns of the COPY block currently being read.
func (s *SQLDumpSource) parseCopyHeader(line string) {
	line = strings.TrimSpace(line)
	rest := strings.TrimSpace(line[len("COPY"):])
	end := strings.IndexAny(rest, " (")
	if end < 0 {
		s.copyTable, s.copyCols = "", nil
		return
	}
	table := unquoteIdent(rest[:end])
	rest = strings.TrimSpace(rest[end:])
	var cols []string
	if strings.HasPrefix(rest, "(") {
		if close := strings.Index(rest, ")"); close >= 0 {
			for _, c := range strings.Split(rest[1:close], ",") {
				cols = append(cols, unquoteIdent(c))
			}
		}
	} else if declared, ok := s.columns[table]; ok {
		for _, c := range declared {
			cols = append(cols, c.Name)
		}
	}
	if _, seen := s.columns[table]; !seen {
		s.order = append(s.order, table)
		decl := make([]Column, len(cols))
		for i, c := range cols {
			decl[i] = Column{Name: c}
		}
		s.columns[table] = decl
	}
	s.copyTable, s.copyCols = table, cols
}

func (s *SQLDumpSource) parseCopyRow(line string) {
	if s.copyTable == "" || strings.TrimSpace(line) == "" {
		return
	}
	fields := strings.Split(line, "\t")
	vals := make(map[string]string, len(s.copyCols))
	nulls := make(map[string]bool)
	for i, c := range s.copyCols {
		if i < len(fields) {
			f := fields[i]
			if f == `\N` {
				nulls[c] = true
			} else {
				vals[c] = decodeCopyField(f)
			}
		} else {
			nulls[c] = true
		}
	}
	s.records = append(s.records, Record{
		Table:  s.copyTable,
		Values: vals,
		Nulls:  nulls,
		Row:    s.tableRowCount(s.copyTable),
	})
}

func decodeCopyField(f string) string {
	r := strings.NewReplacer(`\t`, "\t", `\n`, "\n", `\r`, "\r", `\\`, `\`)
	return r.Replace(f)
}

// splitStatements splits on ';' that is outside single/double quotes, honoring
// the backslash and doubled-quote escapes used by MySQL/PG dumps.
func splitStatements(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			b.WriteRune(c)
			if c == '\\' && i+1 < len(runes) {
				i++
				b.WriteRune(runes[i])
				continue
			}
			if c == quote {
				if i+1 < len(runes) && runes[i+1] == quote { // doubled quote
					i++
					b.WriteRune(runes[i])
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			b.WriteRune(c)
		case ';':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(c)
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"")
	if dot := strings.LastIndex(s, "."); dot >= 0 { // schema.table -> table
		s = s[dot+1:]
	}
	return strings.Trim(s, "`\"")
}

// parseCreateTable records column order; column types are best-effort.
func (s *SQLDumpSource) parseCreateTable(stmt string) {
	open := strings.Index(stmt, "(")
	if open < 0 {
		return
	}
	head := stmt[:open]
	fields := strings.Fields(head) // CREATE TABLE <name>
	if len(fields) < 3 {
		return
	}
	table := unquoteIdent(fields[2])
	body := stmt[open+1:]
	if close := strings.LastIndex(body, ")"); close >= 0 {
		body = body[:close]
	}
	var cols []Column
	for _, line := range strings.Split(body, ",") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		up := strings.ToUpper(line)
		if strings.HasPrefix(up, "PRIMARY") || strings.HasPrefix(up, "KEY") ||
			strings.HasPrefix(up, "UNIQUE") || strings.HasPrefix(up, "CONSTRAINT") ||
			strings.HasPrefix(up, "FOREIGN") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		name := unquoteIdent(parts[0])
		typ := ""
		if len(parts) > 1 {
			typ = strings.ToLower(parts[1])
		}
		cols = append(cols, Column{Name: name, Type: normalizeSQLType(typ)})
	}
	if len(cols) > 0 {
		if _, seen := s.columns[table]; !seen {
			s.order = append(s.order, table)
		}
		s.columns[table] = cols
	}
}

func normalizeSQLType(t string) string {
	switch {
	case t == "":
		return ""
	case strings.HasPrefix(t, "int") || strings.HasPrefix(t, "bigint") ||
		strings.HasPrefix(t, "smallint") || strings.HasPrefix(t, "serial"):
		return "integer"
	case strings.HasPrefix(t, "float") || strings.HasPrefix(t, "double") ||
		strings.HasPrefix(t, "numeric") || strings.HasPrefix(t, "decimal") ||
		strings.HasPrefix(t, "real"):
		return "number"
	case strings.HasPrefix(t, "timestamp") || strings.HasPrefix(t, "date") ||
		strings.HasPrefix(t, "datetime"):
		return "timestamp"
	default:
		return "text"
	}
}

// parseInsert handles: INSERT INTO <table> [(c1,c2,...)] VALUES (..),(..) ;
func (s *SQLDumpSource) parseInsert(stmt string) error {
	rest := strings.TrimSpace(stmt[len("INSERT INTO"):])
	// table name = up to first space or '('
	end := strings.IndexAny(rest, " (")
	if end < 0 {
		return fmt.Errorf("malformed insert")
	}
	table := unquoteIdent(rest[:end])
	rest = strings.TrimSpace(rest[end:])

	var cols []string
	if strings.HasPrefix(rest, "(") {
		close := strings.Index(rest, ")")
		if close < 0 {
			return fmt.Errorf("unterminated column list")
		}
		for _, c := range strings.Split(rest[1:close], ",") {
			cols = append(cols, unquoteIdent(c))
		}
		rest = strings.TrimSpace(rest[close+1:])
	} else if declared, ok := s.columns[table]; ok {
		for _, c := range declared {
			cols = append(cols, c.Name)
		}
	} else {
		return fmt.Errorf("no column list and no CREATE TABLE for %q", table)
	}

	vi := strings.Index(strings.ToUpper(rest), "VALUES")
	if vi < 0 {
		return fmt.Errorf("missing VALUES")
	}
	tuples := parseValueTuples(rest[vi+len("VALUES"):])
	if len(tuples) == 0 {
		return fmt.Errorf("no value tuples")
	}
	if _, seen := s.columns[table]; !seen {
		s.order = append(s.order, table)
		decl := make([]Column, len(cols))
		for i, c := range cols {
			decl[i] = Column{Name: c}
		}
		s.columns[table] = decl
	}
	startRow := s.tableRowCount(table)
	for ti, tuple := range tuples {
		vals := make(map[string]string, len(cols))
		nulls := make(map[string]bool)
		for i, c := range cols {
			if i < len(tuple) {
				if tuple[i].isNull {
					nulls[c] = true
				} else {
					vals[c] = tuple[i].value
				}
			} else {
				nulls[c] = true
			}
		}
		s.records = append(s.records, Record{Table: table, Values: vals, Nulls: nulls, Row: startRow + ti})
	}
	return nil
}

func (s *SQLDumpSource) tableRowCount(table string) int {
	n := 0
	for _, r := range s.records {
		if r.Table == table {
			n++
		}
	}
	return n
}

type cell struct {
	value  string
	isNull bool
}

// parseValueTuples parses "(a,b),(c,d)" into tuples of cells, honoring quotes,
// doubled-quote and backslash escapes, and bareword NULL.
func parseValueTuples(s string) [][]cell {
	var tuples [][]cell
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		for i < len(runes) && runes[i] != '(' {
			i++
		}
		if i >= len(runes) {
			break
		}
		i++ // skip '('
		var tuple []cell
		var b strings.Builder
		var quote rune
		quoted := false
		flush := func() {
			raw := b.String()
			b.Reset()
			if !quoted && strings.EqualFold(strings.TrimSpace(raw), "NULL") {
				tuple = append(tuple, cell{isNull: true})
			} else if quoted {
				tuple = append(tuple, cell{value: raw})
			} else {
				tuple = append(tuple, cell{value: strings.TrimSpace(raw)})
			}
			quoted = false
		}
		for i < len(runes) {
			c := runes[i]
			if quote != 0 {
				if c == '\\' && i+1 < len(runes) {
					next := runes[i+1]
					switch next {
					case 'n':
						b.WriteRune('\n')
					case 't':
						b.WriteRune('\t')
					case 'r':
						b.WriteRune('\r')
					default:
						b.WriteRune(next)
					}
					i += 2
					continue
				}
				if c == quote {
					if i+1 < len(runes) && runes[i+1] == quote {
						b.WriteRune(quote)
						i += 2
						continue
					}
					quote = 0
					i++
					continue
				}
				b.WriteRune(c)
				i++
				continue
			}
			switch c {
			case '\'', '"':
				quote = c
				quoted = true
				i++
			case ',':
				flush()
				i++
			case ')':
				flush()
				i++
				tuples = append(tuples, tuple)
				goto nextTuple
			default:
				b.WriteRune(c)
				i++
			}
		}
	nextTuple:
	}
	return tuples
}

func (s *SQLDumpSource) Schemas(_ context.Context) ([]Schema, error) {
	var out []Schema
	for _, table := range s.order {
		cols := s.columns[table]
		var sample []Record
		for _, r := range s.records {
			if r.Table == table {
				sample = append(sample, r)
				if len(sample) >= s.sampleSize {
					break
				}
			}
		}
		out = append(out, Schema{Table: table, Columns: cols, Sample: sample})
	}
	return out, nil
}

func (s *SQLDumpSource) Records(ctx context.Context, fn func(Record) error) error {
	for _, r := range s.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLDumpSource) Close() error { return nil }
