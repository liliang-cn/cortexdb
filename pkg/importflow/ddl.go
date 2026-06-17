package importflow

import (
	"fmt"
	"strings"
)

// DDLColumn is one declared column.
type DDLColumn struct {
	Name string
	Type string // normalized: text/integer/number/timestamp/"" (unknown)
}

// DDLForeignKey is one foreign key: Column references RefTable(RefColumn).
type DDLForeignKey struct {
	Column    string
	RefTable  string
	RefColumn string
}

// DDLTable is one parsed CREATE TABLE statement.
type DDLTable struct {
	Name        string
	Columns     []DDLColumn
	PrimaryKey  []string
	ForeignKeys []DDLForeignKey
}

// ParseDDL parses a practical subset of Postgres/MySQL CREATE TABLE statements
// (columns, PRIMARY KEY, FOREIGN KEY/REFERENCES). Statements it cannot parse are
// skipped; it errors only when no CREATE TABLE was found at all.
func ParseDDL(ddl string) ([]DDLTable, error) {
	var tables []DDLTable
	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(stmt), "CREATE TABLE") {
			continue
		}
		if tb, ok := parseDDLCreateTable(stmt); ok {
			tables = append(tables, tb)
		}
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("importflow: no CREATE TABLE statements parsed")
	}
	return tables, nil
}

func parseDDLCreateTable(stmt string) (DDLTable, bool) {
	open := strings.Index(stmt, "(")
	closeIdx := strings.LastIndex(stmt, ")")
	if open < 0 || closeIdx <= open {
		return DDLTable{}, false
	}
	header := strings.TrimSpace(stmt[:open]) // CREATE TABLE [IF NOT EXISTS] name
	body := stmt[open+1 : closeIdx]

	fields := strings.Fields(header)
	if len(fields) == 0 {
		return DDLTable{}, false
	}
	name := ddlUnquoteIdent(fields[len(fields)-1])
	tb := DDLTable{Name: name}

	for _, item := range ddlSplitTopLevelCommas(body) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		upper := strings.ToUpper(item)
		switch {
		case strings.HasPrefix(upper, "PRIMARY KEY"):
			tb.PrimaryKey = ddlIdentsInParens(item)
		case strings.HasPrefix(upper, "FOREIGN KEY"):
			if fk, ok := parseDDLForeignKey(item); ok {
				tb.ForeignKeys = append(tb.ForeignKeys, fk)
			}
		case strings.HasPrefix(upper, "CONSTRAINT"):
			if idx := strings.Index(upper, "FOREIGN KEY"); idx >= 0 {
				if fk, ok := parseDDLForeignKey(item[idx:]); ok {
					tb.ForeignKeys = append(tb.ForeignKeys, fk)
				}
			} else if idx := strings.Index(upper, "PRIMARY KEY"); idx >= 0 {
				tb.PrimaryKey = ddlIdentsInParens(item[idx:])
			}
		case strings.HasPrefix(upper, "UNIQUE"), strings.HasPrefix(upper, "CHECK"),
			strings.HasPrefix(upper, "KEY "), strings.HasPrefix(upper, "INDEX "):
			// table-level constraint we don't model; skip
		default:
			col, pk, fk, ok := parseDDLColumnDef(item)
			if !ok {
				continue
			}
			tb.Columns = append(tb.Columns, col)
			if pk {
				tb.PrimaryKey = append(tb.PrimaryKey, col.Name)
			}
			if fk != nil {
				fk.Column = col.Name
				tb.ForeignKeys = append(tb.ForeignKeys, *fk)
			}
		}
	}
	return tb, true
}

func parseDDLColumnDef(item string) (col DDLColumn, isPK bool, fk *DDLForeignKey, ok bool) {
	fields := strings.Fields(item)
	if len(fields) < 1 {
		return DDLColumn{}, false, nil, false
	}
	col.Name = ddlUnquoteIdent(fields[0])
	if len(fields) >= 2 {
		col.Type = normalizeDDLType(fields[1])
	}
	upper := strings.ToUpper(item)
	if strings.Contains(upper, "PRIMARY KEY") {
		isPK = true
	}
	if idx := strings.Index(upper, "REFERENCES"); idx >= 0 {
		if f, fok := parseDDLReferences(item[idx:]); fok {
			fk = &f
		}
	}
	return col, isPK, fk, true
}

func parseDDLForeignKey(item string) (DDLForeignKey, bool) {
	cols := ddlIdentsInParens(item)
	if len(cols) == 0 {
		return DDLForeignKey{}, false
	}
	upper := strings.ToUpper(item)
	idx := strings.Index(upper, "REFERENCES")
	if idx < 0 {
		return DDLForeignKey{}, false
	}
	ref, ok := parseDDLReferences(item[idx:])
	if !ok {
		return DDLForeignKey{}, false
	}
	ref.Column = cols[0]
	return ref, true
}

func parseDDLReferences(item string) (DDLForeignKey, bool) {
	rest := strings.TrimSpace(item[len("REFERENCES"):])
	p := strings.Index(rest, "(")
	if p < 0 {
		return DDLForeignKey{}, false
	}
	refTable := ddlUnquoteIdent(strings.TrimSpace(rest[:p]))
	cols := ddlIdentsInParens(rest[p:])
	if refTable == "" || len(cols) == 0 {
		return DDLForeignKey{}, false
	}
	return DDLForeignKey{RefTable: refTable, RefColumn: cols[0]}, true
}

func ddlIdentsInParens(s string) []string {
	open := strings.Index(s, "(")
	if open < 0 {
		return nil
	}
	depth, end := 0, -1
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s[open+1:end], ",") {
		if id := ddlUnquoteIdent(strings.TrimSpace(part)); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func ddlSplitTopLevelCommas(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func ddlUnquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"[]")
	if dot := strings.LastIndex(s, "."); dot >= 0 {
		s = s[dot+1:]
		s = strings.Trim(s, "`\"[]")
	}
	if p := strings.Index(s, "("); p >= 0 {
		s = s[:p]
	}
	return strings.TrimSpace(s)
}

func normalizeDDLType(t string) string {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "char"), strings.Contains(t, "text"), strings.Contains(t, "uuid"),
		strings.Contains(t, "json"), strings.Contains(t, "enum"):
		return "text"
	case strings.Contains(t, "int"), strings.Contains(t, "serial"):
		return "integer"
	case strings.Contains(t, "numeric"), strings.Contains(t, "decimal"), strings.Contains(t, "real"),
		strings.Contains(t, "double"), strings.Contains(t, "float"):
		return "number"
	case strings.Contains(t, "time"), strings.Contains(t, "date"):
		return "timestamp"
	default:
		return ""
	}
}
