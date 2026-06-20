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
	idx := map[string]int{} // upper(name) -> index in tables
	var alters []string
	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		u := strings.ToUpper(stmt)
		switch {
		case strings.HasPrefix(u, "CREATE TABLE"):
			if tb, ok := parseDDLCreateTable(stmt); ok {
				idx[strings.ToUpper(tb.Name)] = len(tables)
				tables = append(tables, tb)
			}
		case strings.HasPrefix(u, "ALTER TABLE"):
			alters = append(alters, stmt) // FKs are commonly declared out-of-line
		}
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("importflow: no CREATE TABLE statements parsed")
	}
	// Second pass: attach FOREIGN KEYs declared via ALTER TABLE ... ADD ... .
	for _, stmt := range alters {
		name, fk, ok := parseDDLAlterForeignKey(stmt)
		if !ok {
			continue
		}
		if i, exists := idx[strings.ToUpper(name)]; exists {
			tables[i].ForeignKeys = append(tables[i].ForeignKeys, fk)
		}
	}
	return tables, nil
}

// parseDDLAlterForeignKey extracts a foreign key from an
// "ALTER TABLE <name> ADD [CONSTRAINT x] FOREIGN KEY (col) REFERENCES t(col)".
func parseDDLAlterForeignKey(stmt string) (string, DDLForeignKey, bool) {
	upper := strings.ToUpper(stmt)
	if !strings.HasPrefix(upper, "ALTER TABLE") {
		return "", DDLForeignKey{}, false
	}
	fkIdx := strings.Index(upper, "FOREIGN KEY")
	if fkIdx < 0 {
		return "", DDLForeignKey{}, false
	}
	fields := strings.Fields(strings.TrimSpace(stmt[len("ALTER TABLE"):]))
	// skip optional ONLY / IF EXISTS qualifiers before the table name
	for len(fields) > 0 {
		switch strings.ToUpper(fields[0]) {
		case "ONLY", "IF", "EXISTS":
			fields = fields[1:]
		default:
			goto name
		}
	}
name:
	if len(fields) == 0 {
		return "", DDLForeignKey{}, false
	}
	tname := ddlUnquoteIdent(fields[0])
	fk, ok := parseDDLForeignKey(stmt[fkIdx:])
	if !ok {
		return "", DDLForeignKey{}, false
	}
	return tname, fk, true
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

// DDLMappingOptions tunes MappingFromDDL.
type DDLMappingOptions struct {
	// RelationStyle picks the relation predicate from a foreign key:
	//   "" / "column": strip a trailing _id from the FK column (customer_id -> "customer"),
	//                  fallback "references_<reftable>".
	//   "reftable":    predicate = "references_<reftable>".
	RelationStyle string
	IncludeRAG    *bool // default true
	IncludeKG     *bool // default true
}

// MappingFromDDL parses DDL and derives a deterministic MappingPlan: tables ->
// entities (PK -> id), foreign keys -> referenced entities + relations, non-key
// columns -> RAG content + entity props. It also returns the parsed tables.
func MappingFromDDL(ddl string, opts DDLMappingOptions) (MappingPlan, []DDLTable, error) {
	tables, err := ParseDDL(ddl)
	if err != nil {
		return MappingPlan{}, nil, err
	}
	includeRAG := opts.IncludeRAG == nil || *opts.IncludeRAG
	includeKG := opts.IncludeKG == nil || *opts.IncludeKG

	plan := MappingPlan{Tables: map[string]TablePlan{}}
	for _, tb := range tables {
		idCol := ""
		if len(tb.PrimaryKey) == 1 {
			idCol = tb.PrimaryKey[0]
		}
		fkCols := map[string]bool{}
		for _, fk := range tb.ForeignKeys {
			fkCols[fk.Column] = true
		}
		pkCols := map[string]bool{}
		for _, c := range tb.PrimaryKey {
			pkCols[c] = true
		}

		var tp TablePlan
		if includeRAG {
			tp.RAG = &RAGPlan{IDColumn: idCol, ContentTmpl: ddlRAGTemplate(tb, fkCols, pkCols)}
		}
		if includeKG {
			tp.KG = ddlKGPlan(tb, idCol, fkCols, pkCols, opts)
		}
		plan.Tables[tb.Name] = tp
	}
	return plan, tables, nil
}

// ddlRAGTemplate joins non-PK, non-FK columns as "{col}"; falls back to all columns.
func ddlRAGTemplate(tb DDLTable, fkCols, pkCols map[string]bool) string {
	var parts []string
	for _, c := range tb.Columns {
		if pkCols[c.Name] || fkCols[c.Name] {
			continue
		}
		parts = append(parts, "{"+c.Name+"}")
	}
	if len(parts) == 0 {
		for _, c := range tb.Columns {
			parts = append(parts, "{"+c.Name+"}")
		}
	}
	return strings.Join(parts, " ")
}

// ddlKGPlan builds the KG plan: the table's own entity (if it has a single-column
// PK) plus one referenced entity + relation per foreign key.
func ddlKGPlan(tb DDLTable, idCol string, fkCols, pkCols map[string]bool, opts DDLMappingOptions) *KGPlan {
	kg := &KGPlan{}
	used := map[string]bool{} // entity refs already taken (avoid self-ref/dup collisions)

	if idCol != "" {
		var props []string
		for _, c := range tb.Columns {
			if pkCols[c.Name] || fkCols[c.Name] {
				continue
			}
			props = append(props, c.Name)
		}
		kg.Entities = append(kg.Entities, EntityMap{
			Ref: tb.Name, Type: ddlTypeName(tb.Name), IDTmpl: "{" + idCol + "}", Props: props,
		})
		used[tb.Name] = true
	}

	for _, fk := range tb.ForeignKeys {
		ref := ddlUniqueRef(fk.RefTable, used)
		kg.Entities = append(kg.Entities, EntityMap{
			Ref: ref, Type: ddlTypeName(fk.RefTable), IDTmpl: "{" + fk.Column + "}",
		})
		kg.Relations = append(kg.Relations, RelationMap{
			Subject: tb.Name, Predicate: ddlPredicateFor(fk, opts), Object: ref,
		})
	}
	if len(kg.Entities) == 0 && len(kg.Relations) == 0 {
		return nil
	}
	return kg
}

func ddlUniqueRef(base string, used map[string]bool) string {
	ref := base
	for n := 2; used[ref]; n++ {
		ref = fmt.Sprintf("%s#%d", base, n)
	}
	used[ref] = true
	return ref
}

func ddlPredicateFor(fk DDLForeignKey, opts DDLMappingOptions) string {
	if strings.EqualFold(opts.RelationStyle, "reftable") {
		return "references_" + fk.RefTable
	}
	base := fk.Column
	for _, suf := range []string{"_id", "_ID", "Id", "ID"} {
		if strings.HasSuffix(base, suf) && len(base) > len(suf) {
			return base[:len(base)-len(suf)]
		}
	}
	if base != "" {
		return base
	}
	return "references_" + fk.RefTable
}

// ddlTypeName singularizes (naive: strip one trailing 's') and TitleCases a table
// name: orders -> Order, customers -> Customer.
func ddlTypeName(table string) string {
	t := table
	if strings.HasSuffix(t, "s") && len(t) > 1 {
		t = t[:len(t)-1]
	}
	if t == "" {
		return table
	}
	return strings.ToUpper(t[:1]) + t[1:]
}
