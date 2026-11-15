// pkg/importflow/types.go
package importflow

import (
	"encoding/json"
	"errors"
)

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
	// BuildTables also keeps every table as a table — rows and typed columns
	// — that cortexdb.DB.QueryImportedTables can aggregate and join. The
	// inferer is not asked; Plan adds it to whatever it proposed.
	BuildTables bool
	Hint        string // domain hint passed to the AI inferer
}

// Report summarizes an import run. Per repo "no silent caps" rule, dropped /
// unparsed input is surfaced here rather than discarded silently.
type Report struct {
	RowsRead           int
	ChunksIndexed      int
	TriplesCreated     int
	TablesStored       int // tables kept as queryable tables
	TableRowsStored    int // rows in those tables
	Skipped            int
	UnparsedStatements []string
	Errors             []error
}

func (rep *Report) addError(err error) {
	if err != nil {
		rep.Errors = append(rep.Errors, err)
	}
}

// reportJSON is a Report on the wire: the same fields under the same names,
// with each error as its message.
type reportJSON struct {
	RowsRead           int
	ChunksIndexed      int
	TriplesCreated     int
	TablesStored       int
	TableRowsStored    int
	Skipped            int
	UnparsedStatements []string
	Errors             []string
}

// MarshalJSON writes Errors as messages. An error value has no fields for
// encoding/json to write, so a tool that returned a Report told its caller
// that rows had failed and not why: a list of empty objects.
func (rep Report) MarshalJSON() ([]byte, error) {
	out := reportJSON{
		RowsRead:           rep.RowsRead,
		ChunksIndexed:      rep.ChunksIndexed,
		TriplesCreated:     rep.TriplesCreated,
		TablesStored:       rep.TablesStored,
		TableRowsStored:    rep.TableRowsStored,
		Skipped:            rep.Skipped,
		UnparsedStatements: rep.UnparsedStatements,
	}
	for _, err := range rep.Errors {
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads what MarshalJSON writes.
func (rep *Report) UnmarshalJSON(data []byte) error {
	var in reportJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*rep = Report{
		RowsRead:           in.RowsRead,
		ChunksIndexed:      in.ChunksIndexed,
		TriplesCreated:     in.TriplesCreated,
		TablesStored:       in.TablesStored,
		TableRowsStored:    in.TableRowsStored,
		Skipped:            in.Skipped,
		UnparsedStatements: in.UnparsedStatements,
	}
	for _, msg := range in.Errors {
		rep.Errors = append(rep.Errors, errors.New(msg))
	}
	return nil
}
