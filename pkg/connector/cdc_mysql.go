package connector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// MySQLBinlogOptions configures the binlog change source.
type MySQLBinlogOptions struct {
	ServerID uint32              // unique replica id for this client (required; default 1001)
	Tables   map[string][]string // table -> PK column names (optional; falls back to Table.PKColumns)
}

type mysqlBinlogSource struct {
	cfg      *canal.Config
	dbName   string
	opts     MySQLBinlogOptions
	canal    *canal.Canal
	startPos mysql.Position
}

// NewMySQLBinlogSource parses the (go-sql-driver) DSN and prepares a binlog
// reader. It does not start streaming until Changes is called.
func NewMySQLBinlogSource(dsn string, opts MySQLBinlogOptions) (ChangeSource, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("connector: mysql binlog DSN is required")
	}
	mc, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("connector: parse mysql dsn: %w", err)
	}
	if opts.ServerID == 0 {
		opts.ServerID = 1001
	}
	cfg := canal.NewDefaultConfig()
	cfg.Addr = mc.Addr
	cfg.User = mc.User
	cfg.Password = mc.Passwd
	cfg.ServerID = opts.ServerID
	cfg.Flavor = "mysql"
	cfg.Dump.ExecutionPath = "" // binlog-only; no initial mysqldump snapshot
	if mc.DBName != "" {
		var inc []string
		for table := range opts.Tables {
			inc = append(inc, regexpEscape(mc.DBName)+"\\."+regexpEscape(table))
		}
		cfg.IncludeTableRegex = inc
	}
	return &mysqlBinlogSource{cfg: cfg, dbName: mc.DBName, opts: opts}, nil
}

func regexpEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`.\+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *mysqlBinlogSource) LoadPosition(pos string) error {
	if pos == "" {
		return nil
	}
	i := strings.LastIndex(pos, ":")
	if i < 0 {
		return fmt.Errorf("connector: bad binlog position %q (want file:pos)", pos)
	}
	off, err := strconv.ParseUint(pos[i+1:], 10, 32)
	if err != nil {
		return fmt.Errorf("connector: bad binlog offset in %q: %w", pos, err)
	}
	s.startPos = mysql.Position{Name: pos[:i], Pos: uint32(off)}
	return nil
}

func (s *mysqlBinlogSource) Close() error {
	if s.canal != nil {
		s.canal.Close()
	}
	return nil
}

func (s *mysqlBinlogSource) Changes(ctx context.Context, fn func(ChangeEvent) error) error {
	c, err := canal.NewCanal(s.cfg)
	if err != nil {
		return fmt.Errorf("connector: new canal: %w", err)
	}
	s.canal = c

	start := s.startPos
	if start.Name == "" {
		start, err = c.GetMasterPos()
		if err != nil {
			c.Close()
			return fmt.Errorf("connector: master pos: %w", err)
		}
	}
	h := &binlogHandler{fn: fn, keyCols: s.opts.Tables, curFile: start.Name}
	c.SetEventHandler(h)

	go func() {
		<-ctx.Done()
		c.Close()
	}()

	err = c.RunFrom(start)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if h.err != nil {
		return h.err
	}
	return err
}

type binlogHandler struct {
	canal.DummyEventHandler
	fn      func(ChangeEvent) error
	keyCols map[string][]string
	mu      sync.Mutex
	curFile string
	err     error
}

func (h *binlogHandler) String() string { return "connector-binlog" }

func (h *binlogHandler) OnRotate(_ *replication.EventHeader, r *replication.RotateEvent) error {
	h.mu.Lock()
	h.curFile = string(r.NextLogName)
	h.mu.Unlock()
	return nil
}

func (h *binlogHandler) OnRow(e *canal.RowsEvent) error {
	h.mu.Lock()
	pos := h.curFile + ":" + strconv.FormatUint(uint64(e.Header.LogPos), 10)
	h.mu.Unlock()
	switch e.Action {
	case canal.InsertAction:
		for _, row := range e.Rows {
			if err := h.emit(e.Table, row, OpInsert, pos); err != nil {
				h.err = err
				return err
			}
		}
	case canal.DeleteAction:
		for _, row := range e.Rows {
			if err := h.emit(e.Table, row, OpDelete, pos); err != nil {
				h.err = err
				return err
			}
		}
	case canal.UpdateAction:
		for i := 0; i+1 < len(e.Rows); i += 2 {
			if err := h.emit(e.Table, e.Rows[i+1], OpUpdate, pos); err != nil {
				h.err = err
				return err
			}
		}
	}
	return nil
}

func (h *binlogHandler) emit(table *schema.Table, row []any, op ChangeOp, pos string) error {
	values := map[string]string{}
	nulls := map[string]bool{}
	for j, col := range table.Columns {
		if j >= len(row) {
			break
		}
		if row[j] == nil {
			nulls[col.Name] = true
			continue
		}
		values[col.Name] = valueToString(row[j])
	}
	key := map[string]string{}
	if pk := h.keyCols[table.Name]; len(pk) > 0 {
		for _, name := range pk {
			if v, ok := values[name]; ok {
				key[name] = v
			}
		}
	} else {
		for _, idx := range table.PKColumns {
			if idx < len(table.Columns) {
				name := table.Columns[idx].Name
				if v, ok := values[name]; ok {
					key[name] = v
				}
			}
		}
	}
	return h.fn(ChangeEvent{
		Op:       op,
		Table:    table.Name,
		Key:      key,
		Row:      importflow.Record{Table: table.Name, Values: values, Nulls: nulls},
		Position: pos,
	})
}
