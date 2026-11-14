package core

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// A read that the engine itself confines.
//
// The brain's file holds memories, documents and tokens next to whatever
// tables a caller imported, and a tool that runs SQL written by a model must
// reach the second kind and none of the first. Reading the SQL text to decide
// that is not a control: the text has comments, quoting, CTEs and a grammar
// larger than any pattern over it. SQLite already decides it, exactly, while
// it compiles a statement — it calls an authorizer for every table and column
// the statement reads, every function it calls and every other thing it would
// do, before a single row moves. RunGuardedSQLiteRead installs one for the
// length of one query.
//
// modernc.org/sqlite (v1.38) does not expose sqlite3_set_authorizer on its
// connection, but the transpiled library it is built on does, as
// sqlite3.Xsqlite3_set_authorizer. What is missing is only the connection's
// handle, which is two unexported fields. They are read by reflection in
// sqliteHandleOf, and that is the one fragile joint here: a driver release
// that renames them makes every guarded read fail with a clear error — it
// cannot make one run unguarded, because nothing runs before the authorizer
// is in place — and TestGuardedReadReachesTheDriverHandle fails first.

// ErrGuardedReadRefused is wrapped by every error that means the query asked
// for something a guarded read does not do.
var ErrGuardedReadRefused = errors.New("query refused")

// ErrGuardedReadTimeout is wrapped when a query outlives its time limit.
var ErrGuardedReadTimeout = errors.New("query exceeded its time limit")

// GuardedRead is one read-only query and the tables it may read.
type GuardedRead struct {
	// SQL is a single statement.
	SQL string
	// Args are positional values for `?` placeholders.
	Args []any
	// Tables maps each name the query may use to the table in the main
	// schema that holds its rows. Nothing else in the database is readable.
	Tables map[string]string
	// MaxRows caps the rows returned; one more is read to learn whether the
	// answer was cut.
	MaxRows int
	// Timeout bounds the whole query.
	Timeout time.Duration
}

// GuardedRows is a guarded read's answer.
type GuardedRows struct {
	Columns   []string
	Rows      [][]any
	Truncated bool
}

// RunGuardedSQLiteRead runs q on one connection of db with three engine-level
// controls in force, and removes them before the connection goes back:
//
//   - an authorizer that allows SELECT, reads of the tables in q.Tables (by
//     the given name, through a temp view, or by the physical name), function
//     calls other than load_extension, and recursive CTEs — and denies every
//     other action code, which is every write, every DDL statement, PRAGMA,
//     ATTACH, DETACH, transaction control, and every read of another table,
//     sqlite_master and the virtual tables included;
//   - PRAGMA query_only, so a write would fail at the pager even with no
//     authorizer;
//   - a progress handler that interrupts the statement at the deadline.
//
// Before anything runs the text is compiled statement by statement: more than
// one statement, or one SQLite itself does not call read-only, is refused.
//
// The whole read happens inside one read transaction, so the schema the
// checks were made against is the schema the query runs against.
func RunGuardedSQLiteRead(ctx context.Context, db *sql.DB, q GuardedRead) (*GuardedRows, error) {
	if db == nil {
		return nil, errors.New("guarded read: no database")
	}
	if strings.TrimSpace(q.SQL) == "" {
		return nil, fmt.Errorf("%w: empty query", ErrGuardedReadRefused)
	}
	if strings.ContainsRune(q.SQL, 0) {
		return nil, fmt.Errorf("%w: query contains a NUL byte", ErrGuardedReadRefused)
	}
	if q.MaxRows <= 0 || q.Timeout <= 0 {
		return nil, errors.New("guarded read: MaxRows and Timeout are required")
	}
	args := make([]driver.NamedValue, len(q.Args))
	for i, a := range q.Args {
		v, err := driver.DefaultParameterConverter.ConvertValue(a)
		if err != nil {
			return nil, fmt.Errorf("guarded read: parameter %d: %w", i+1, err)
		}
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	g := &readGuard{views: map[string]bool{}, physical: map[string]bool{}, unresolved: map[string]bool{}, ctes: map[string]bool{}}
	for name, physical := range q.Tables {
		if name == "" || physical == "" || strings.ContainsRune(name, 0) || strings.ContainsRune(physical, 0) {
			return nil, fmt.Errorf("guarded read: unusable table name %q", name)
		}
		g.views[asciiLower(name)] = true
		g.physical[asciiLower(physical)] = true
	}

	ctx, cancel := context.WithTimeout(ctx, q.Timeout)
	defer cancel()
	g.ctx = ctx

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	var out *GuardedRows
	var runErr error
	rawErr := conn.Raw(func(dc any) error {
		var dirty bool
		out, dirty, runErr = g.run(ctx, dc, q, args)
		if dirty {
			// A control could not be taken off again. The connection is not
			// fit for the pool: database/sql closes it on ErrBadConn.
			return driver.ErrBadConn
		}
		return nil
	})
	switch {
	case runErr != nil:
		if g.timedOut.Load() || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w (%s)", ErrGuardedReadTimeout, q.Timeout)
		}
		if g.denied != "" {
			return nil, fmt.Errorf("%w: %s", ErrGuardedReadRefused, g.denied)
		}
		return nil, runErr
	case out == nil:
		return nil, rawErr
	}
	return out, nil
}

// readGuard is the policy of one guarded read and what it observed. Names
// are keyed the way SQLite compares identifiers: ASCII case folded.
type readGuard struct {
	ctx      context.Context
	views    map[string]bool // names in the temp schema
	physical map[string]bool // tables in the main schema
	denied   string          // the first thing the authorizer refused
	timedOut atomic.Bool

	// A table that is named but has no column read — SELECT count(*) FROM x —
	// reaches the authorizer as the name the query wrote and nothing else:
	// no schema, and no way to tell a CTE called x from a table called x.
	// So the first compilation only collects such names (mode guardCollect),
	// each is then put to the engine on its own (guardProbe), and only the
	// ones that resolve to nothing outside the query, which leaves a CTE, are
	// let through when the query is compiled to run (guardEnforce).
	mode       int
	unresolved map[string]bool
	ctes       map[string]bool
}

const (
	guardCollect = iota
	guardProbe
	guardEnforce
)

// run does the whole read on the driver's own connection. dirty reports that
// the connection still carries a control and must not be reused.
func (g *readGuard) run(ctx context.Context, dc any, q GuardedRead, args []driver.NamedValue) (out *GuardedRows, dirty bool, err error) {
	if queued, ok := dc.(*queuedConn); ok {
		dc = queued.Conn
	}
	h, err := sqliteHandleOf(dc)
	if err != nil {
		return nil, false, err
	}
	execer, okE := dc.(driver.ExecerContext)
	queryer, okQ := dc.(driver.QueryerContext)
	if !okE || !okQ {
		return nil, false, fmt.Errorf("guarded read: %T cannot run statements directly", dc)
	}
	// Setup and teardown run without the caller's deadline: a statement that
	// puts a control on or takes it off must not be the one interrupted.
	exec := func(stmt string) error {
		_, err := execer.ExecContext(context.Background(), stmt, nil)
		return err
	}

	// The names the query may use are temp views over the physical tables.
	// The temp schema is searched first, so a name that is also one of the
	// brain's own tables resolves to the imported one.
	var created []string
	defer func() {
		for _, name := range created {
			if e := exec("DROP VIEW IF EXISTS temp." + quoteSQLiteIdent(name)); e != nil {
				dirty = true
			}
		}
	}()
	for name, physical := range q.Tables {
		if err := exec("CREATE TEMP VIEW " + quoteSQLiteIdent(name) + " AS SELECT * FROM main." + quoteSQLiteIdent(physical)); err != nil {
			return nil, false, fmt.Errorf("guarded read: expose table %q: %w", name, err)
		}
		created = append(created, name)
	}

	// One read transaction around the checks and the query, opened by a read
	// so the snapshot — the schema with it — is fixed from here on.
	if err := exec("BEGIN"); err != nil {
		return nil, false, fmt.Errorf("guarded read: %w", err)
	}
	defer func() {
		if e := exec("ROLLBACK"); e != nil {
			dirty = true
		}
	}()
	if err := exec("SELECT 1 FROM main.sqlite_master LIMIT 1"); err != nil {
		return nil, false, fmt.Errorf("guarded read: %w", err)
	}
	if err := exec("PRAGMA query_only = ON"); err != nil {
		return nil, false, fmt.Errorf("guarded read: %w", err)
	}
	defer func() {
		if e := exec("PRAGMA query_only = OFF"); e != nil {
			dirty = true
		}
	}()

	id := guardSeq.Add(1)
	guards.Store(id, g)
	defer guards.Delete(id)
	sqlite3.Xsqlite3_progress_handler(h.tls, h.db, 1000, cFuncPointer(guardProgress), uintptr(id))
	defer sqlite3.Xsqlite3_progress_handler(h.tls, h.db, 0, 0, 0)
	if rc := sqlite3.Xsqlite3_set_authorizer(h.tls, h.db, cFuncPointer(guardAuthorize), uintptr(id)); rc != sqlite3.SQLITE_OK {
		return nil, false, fmt.Errorf("guarded read: install authorizer: code %d", rc)
	}
	defer sqlite3.Xsqlite3_set_authorizer(h.tls, h.db, 0, 0)

	if err := h.compileSingleRead(q.SQL); err != nil {
		return nil, false, err
	}
	if err := g.resolveBareNames(h); err != nil {
		return nil, false, err
	}
	g.mode = guardEnforce

	rows, err := queryer.QueryContext(ctx, q.SQL, args)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if e := rows.Close(); e != nil && err == nil {
			err, out = e, nil
		}
	}()
	out = &GuardedRows{Columns: rows.Columns(), Rows: [][]any{}}
	dest := make([]driver.Value, len(out.Columns))
	for {
		if e := rows.Next(dest); e != nil {
			if e == io.EOF {
				return out, false, nil
			}
			return nil, false, e
		}
		if len(out.Rows) >= q.MaxRows {
			out.Truncated = true
			return out, false, nil
		}
		row := make([]any, len(dest))
		for i, v := range dest {
			if b, ok := v.([]byte); ok {
				v = append([]byte(nil), b...)
			}
			row[i] = v
		}
		out.Rows = append(out.Rows, row)
	}
}

// guards maps the integer handed to SQLite as callback argument back to the
// Go policy. An integer and not a pointer, because the value crosses into
// memory the garbage collector does not scan.
var (
	guards   sync.Map // uint64 -> *readGuard
	guardSeq atomic.Uint64
)

func guardFor(arg uintptr) *readGuard {
	v, ok := guards.Load(uint64(arg))
	if !ok {
		return nil
	}
	return v.(*readGuard)
}

// guardAuthorize is the sqlite3_set_authorizer callback.
func guardAuthorize(_ *libc.TLS, arg uintptr, action int32, z1, z2, z3, _ uintptr) int32 {
	g := guardFor(arg)
	if g == nil {
		return sqlite3.SQLITE_DENY
	}
	return g.authorize(action, goString(z1), goString(z2), goString(z3))
}

// guardProgress is the sqlite3_progress_handler callback; non-zero interrupts.
func guardProgress(_ *libc.TLS, arg uintptr) int32 {
	g := guardFor(arg)
	if g == nil {
		return 1
	}
	if g.ctx.Err() != nil {
		g.timedOut.Store(errors.Is(g.ctx.Err(), context.DeadlineExceeded))
		return 1
	}
	return 0
}

// pureTableFunctions are the table-valued functions a guarded read may call.
// They are reported to the authorizer as tables; these two read only their
// argument.
var pureTableFunctions = map[string]bool{"json_each": true, "json_tree": true}

func (g *readGuard) authorize(action int32, a, b, schema string) int32 {
	if g.mode == guardProbe {
		return sqlite3.SQLITE_OK // compiled to learn whether a name resolves, never run
	}
	switch action {
	case sqlite3.SQLITE_SELECT, sqlite3.SQLITE_RECURSIVE:
		return sqlite3.SQLITE_OK
	case sqlite3.SQLITE_READ:
		name, schema := asciiLower(a), asciiLower(schema)
		switch {
		case schema == "temp" && g.views[name]:
			return sqlite3.SQLITE_OK
		case schema == "main" && g.physical[name]:
			return sqlite3.SQLITE_OK
		case pureTableFunctions[name] && !g.views[name]:
			return sqlite3.SQLITE_OK
		case schema == "" && b == "":
			// Named with no schema: temp is searched before main, so one of
			// the given names is the view, and a physical name is the table.
			if g.views[name] || g.physical[name] || g.ctes[name] {
				return sqlite3.SQLITE_OK
			}
			if g.mode == guardCollect {
				g.unresolved[name] = true
				return sqlite3.SQLITE_OK
			}
		}
		return g.deny(fmt.Sprintf("%q is not an imported table", a))
	case sqlite3.SQLITE_FUNCTION:
		if strings.EqualFold(b, "load_extension") {
			return g.deny("load_extension is not available")
		}
		return sqlite3.SQLITE_OK
	}
	return g.deny("only a single SELECT over imported tables is allowed")
}

func (g *readGuard) deny(why string) int32 {
	if g.denied == "" {
		g.denied = why
	}
	return sqlite3.SQLITE_DENY
}

// resolveBareNames decides each name the first compilation could not place.
// A name that compiles as a table on its own is something in the database —
// a table, a view, an eponymous virtual table — and the query is refused; a
// name SQLite does not know can only have been a CTE inside the query.
func (g *readGuard) resolveBareNames(h sqliteHandle) error {
	g.mode = guardProbe
	for name := range g.unresolved {
		stmt, _, msg, err := h.prepare("SELECT 1 FROM " + quoteSQLiteIdent(name))
		if err != nil {
			return err
		}
		if stmt != 0 {
			sqlite3.Xsqlite3_finalize(h.tls, stmt)
		}
		if stmt != 0 || !strings.HasPrefix(msg, "no such table") {
			g.denied = fmt.Sprintf("%q is not an imported table (a CTE cannot take the name of something the database already holds)", name)
			return fmt.Errorf("%w: %s", ErrGuardedReadRefused, g.denied)
		}
		g.ctes[name] = true
	}
	return nil
}

// sqliteHandle is what the transpiled SQLite API takes: the connection's
// libc thread state and its sqlite3*.
type sqliteHandle struct {
	tls *libc.TLS
	db  uintptr
}

// sqliteHandleOf reads the handle out of a modernc.org/sqlite connection.
// The fields are unexported, so anything unexpected is an error and never a
// guess.
func sqliteHandleOf(dc any) (sqliteHandle, error) {
	unusable := func(why string) (sqliteHandle, error) {
		return sqliteHandle{}, fmt.Errorf("guarded read: the SQLite driver's connection (%T) is not the shape this build knows (%s); the query was not run", dc, why)
	}
	v := reflect.ValueOf(dc)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return unusable("not a struct pointer")
	}
	e := v.Elem()
	if e.Type().PkgPath() != "modernc.org/sqlite" {
		return unusable("not modernc.org/sqlite")
	}
	dbField, tlsField := e.FieldByName("db"), e.FieldByName("tls")
	if !dbField.IsValid() || dbField.Kind() != reflect.Uintptr {
		return unusable("no db handle")
	}
	if !tlsField.IsValid() || tlsField.Type() != reflect.TypeOf((*libc.TLS)(nil)) {
		return unusable("no thread state")
	}
	h := sqliteHandle{tls: (*libc.TLS)(tlsField.UnsafePointer()), db: uintptr(dbField.Uint())}
	if h.tls == nil || h.db == 0 {
		return unusable("connection is closed")
	}
	return h, nil
}

// prepare compiles the first statement of text and finalizes nothing: stmt is
// 0 for whitespace or a comment, rest is what follows the statement, and msg
// is SQLite's message when it did not compile.
func (h sqliteHandle) prepare(text string) (stmt uintptr, rest string, msg string, err error) {
	zSQL, err := libc.CString(text)
	if err != nil {
		return 0, "", "", err
	}
	defer libc.Xfree(h.tls, zSQL)
	const word = int(unsafe.Sizeof(uintptr(0)))
	out := h.tls.Alloc(2 * word)
	defer h.tls.Free(2 * word)
	ppStmt, pzTail := out, out+uintptr(word)
	if rc := sqlite3.Xsqlite3_prepare_v2(h.tls, h.db, zSQL, -1, ppStmt, pzTail); rc != sqlite3.SQLITE_OK {
		return 0, "", libc.GoString(sqlite3.Xsqlite3_errmsg(h.tls, h.db)), nil
	}
	if tail := readWord(pzTail); tail != 0 {
		rest = libc.GoString(tail)
	}
	return readWord(ppStmt), rest, "", nil
}

// compileSingleRead compiles text the way the driver is about to, statement
// by statement, and refuses it unless it is exactly one statement that SQLite
// reports as read-only. The driver would otherwise run every statement in the
// string and return the last one's rows.
func (h sqliteHandle) compileSingleRead(text string) error {
	statements := 0
	for text != "" {
		stmt, rest, msg, err := h.prepare(text)
		if err != nil {
			return err
		}
		if msg != "" {
			return errors.New(msg)
		}
		text = rest
		if stmt == 0 {
			continue // whitespace or a comment
		}
		readOnly := sqlite3.Xsqlite3_stmt_readonly(h.tls, stmt) != 0
		sqlite3.Xsqlite3_finalize(h.tls, stmt)
		statements++
		if statements > 1 {
			return fmt.Errorf("%w: one statement per query", ErrGuardedReadRefused)
		}
		if !readOnly {
			return fmt.Errorf("%w: the statement writes", ErrGuardedReadRefused)
		}
	}
	if statements == 0 {
		return fmt.Errorf("%w: no statement", ErrGuardedReadRefused)
	}
	return nil
}

// readWord reads a pointer-sized value SQLite wrote at p.
func readWord(p uintptr) uintptr {
	b := libc.GoBytes(p, int(unsafe.Sizeof(uintptr(0))))
	if len(b) == 8 {
		return uintptr(binary.NativeEndian.Uint64(b))
	}
	return uintptr(binary.NativeEndian.Uint32(b))
}

func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	return libc.GoString(p)
}

// cFuncPointer turns a top-level Go function into the function pointer the
// transpiled C code calls through, the same way modernc.org/sqlite does for
// its own callbacks: a func value is a pointer to a pointer to code
// (https://golang.org/s/go11func), and for a declared function that outer
// pointer lives in read-only data and never moves. Not for closures.
func cFuncPointer[T any](f T) uintptr {
	return *(*uintptr)(unsafe.Pointer(&struct{ f T }{f}))
}

// asciiLower folds case the way SQLite compares identifiers: ASCII only.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// quoteSQLiteIdent quotes s as an SQL identifier.
func quoteSQLiteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
