// pkg/importflow/types.go
package importflow

// Column is one source column with a best-effort type label.
type Column struct {
	Name string
	Type string // "integer","number","text","timestamp","" (unknown)
}

// Record is one normalized source row.
type Record struct {
	Table  string
	Values map[string]string // column name -> string value
	Nulls  map[string]bool   // column name -> is NULL
	Row    int               // 0-based row index within the table
}

// Get returns the value for col and false when the column is missing or NULL.
func (r Record) Get(col string) (string, bool) {
	if r.Nulls[col] {
		return "", false
	}
	v, ok := r.Values[col]
	return v, ok
}

// Schema describes a table plus sample rows, used for AI mapping inference.
type Schema struct {
	Table   string
	Columns []Column
	Sample  []Record
}

// Goal declares what the import should build.
type Goal struct {
	BuildRAG bool
	BuildKG  bool
	Hint     string // domain hint passed to the AI inferer
}

// Report summarizes an import run. Per repo "no silent caps" rule, dropped /
// unparsed input is surfaced here rather than discarded silently.
type Report struct {
	RowsRead           int
	ChunksIndexed      int
	TriplesCreated     int
	Skipped            int
	UnparsedStatements []string
	Errors             []error
}

func (rep *Report) addError(err error) {
	if err != nil {
		rep.Errors = append(rep.Errors, err)
	}
}
