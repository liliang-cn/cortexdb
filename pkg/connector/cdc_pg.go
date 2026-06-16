package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// PostgresCDCOptions configures the logical-replication change source.
type PostgresCDCOptions struct {
	Publication     string              // pgoutput publication name (required)
	Slot            string              // replication slot name (required)
	Tables          map[string][]string // table -> primary-key column names (for ChangeEvent.Key)
	CreateSlot      bool                // create a permanent slot if absent (recommended true)
	StandbyInterval time.Duration       // standby status cadence; default 10s
}

type postgresCDCSource struct {
	dsn       string
	opts      PostgresCDCOptions
	conn      *pgconn.PgConn
	relations map[uint32]*pglogrepl.RelationMessage
	keyCols   map[string]map[string]bool
	startLSN  pglogrepl.LSN
}

// NewPostgresCDCSource opens a replication connection and prepares (but does not
// start) streaming. "replication=database" is appended to the DSN if absent.
func NewPostgresCDCSource(dsn string, opts PostgresCDCOptions) (ChangeSource, error) {
	if opts.Publication == "" {
		return nil, fmt.Errorf("connector: PostgresCDCOptions.Publication is required")
	}
	if opts.Slot == "" {
		return nil, fmt.Errorf("connector: PostgresCDCOptions.Slot is required")
	}
	if opts.StandbyInterval == 0 {
		opts.StandbyInterval = 10 * time.Second
	}
	replDSN := dsn
	if !strings.Contains(replDSN, "replication=") {
		sep := "?"
		if strings.Contains(replDSN, "?") {
			sep = "&"
		}
		replDSN = replDSN + sep + "replication=database"
	}
	conn, err := pgconn.Connect(context.Background(), replDSN)
	if err != nil {
		return nil, fmt.Errorf("connector: pg replication connect: %w", err)
	}
	keyCols := map[string]map[string]bool{}
	for table, pk := range opts.Tables {
		set := map[string]bool{}
		for _, c := range pk {
			set[c] = true
		}
		keyCols[table] = set
	}
	return &postgresCDCSource{
		dsn: replDSN, opts: opts, conn: conn,
		relations: map[uint32]*pglogrepl.RelationMessage{}, keyCols: keyCols,
	}, nil
}

func (s *postgresCDCSource) LoadPosition(pos string) error {
	if pos == "" {
		return nil
	}
	lsn, err := pglogrepl.ParseLSN(pos)
	if err != nil {
		return fmt.Errorf("connector: bad resume LSN %q: %w", pos, err)
	}
	s.startLSN = lsn
	return nil
}

func (s *postgresCDCSource) Close() error { return s.conn.Close(context.Background()) }

func (s *postgresCDCSource) Changes(ctx context.Context, fn func(ChangeEvent) error) error {
	if s.opts.CreateSlot || s.startLSN == 0 {
		_, err := pglogrepl.CreateReplicationSlot(ctx, s.conn, s.opts.Slot, "pgoutput",
			pglogrepl.CreateReplicationSlotOptions{Mode: pglogrepl.LogicalReplication})
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("connector: create slot: %w", err)
		}
	}
	startLSN := s.startLSN
	if startLSN == 0 {
		sys, err := pglogrepl.IdentifySystem(ctx, s.conn)
		if err != nil {
			return fmt.Errorf("connector: identify system: %w", err)
		}
		startLSN = sys.XLogPos
	}
	if err := pglogrepl.StartReplication(ctx, s.conn, s.opts.Slot, startLSN,
		pglogrepl.StartReplicationOptions{
			Mode:       pglogrepl.LogicalReplication,
			PluginArgs: []string{"proto_version '1'", "publication_names '" + s.opts.Publication + "'"},
		}); err != nil {
		return fmt.Errorf("connector: start replication: %w", err)
	}

	clientXLogPos := startLSN
	nextStandby := time.Now().Add(s.opts.StandbyInterval)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(nextStandby) {
			if err := pglogrepl.SendStandbyStatusUpdate(ctx, s.conn,
				pglogrepl.StandbyStatusUpdate{WALWritePosition: clientXLogPos}); err != nil {
				return fmt.Errorf("connector: standby update: %w", err)
			}
			nextStandby = time.Now().Add(s.opts.StandbyInterval)
		}
		rctx, cancel := context.WithDeadline(ctx, nextStandby)
		raw, err := s.conn.ReceiveMessage(rctx)
		cancel()
		if err != nil {
			if pgconn.Timeout(err) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("connector: receive: %w", err)
		}
		cd, ok := raw.(*pgproto3.CopyData)
		if !ok {
			continue
		}
		switch cd.Data[0] {
		case pglogrepl.PrimaryKeepaliveMessageByteID:
			pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(cd.Data[1:])
			if err != nil {
				return fmt.Errorf("connector: parse keepalive: %w", err)
			}
			if pkm.ServerWALEnd > clientXLogPos {
				clientXLogPos = pkm.ServerWALEnd
			}
			if pkm.ReplyRequested {
				nextStandby = time.Time{}
			}
		case pglogrepl.XLogDataByteID:
			xld, err := pglogrepl.ParseXLogData(cd.Data[1:])
			if err != nil {
				return fmt.Errorf("connector: parse xlogdata: %w", err)
			}
			if err := s.handleWAL(xld, fn); err != nil {
				return err
			}
			clientXLogPos = xld.WALStart + pglogrepl.LSN(len(xld.WALData))
		}
	}
}

func (s *postgresCDCSource) handleWAL(xld pglogrepl.XLogData, fn func(ChangeEvent) error) error {
	msg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		return fmt.Errorf("connector: parse pgoutput: %w", err)
	}
	pos := (xld.WALStart + pglogrepl.LSN(len(xld.WALData))).String()
	switch m := msg.(type) {
	case *pglogrepl.RelationMessage:
		s.relations[m.RelationID] = m
	case *pglogrepl.InsertMessage:
		return s.emit(m.RelationID, m.Tuple, OpInsert, pos, fn)
	case *pglogrepl.UpdateMessage:
		return s.emit(m.RelationID, m.NewTuple, OpUpdate, pos, fn)
	case *pglogrepl.DeleteMessage:
		return s.emit(m.RelationID, m.OldTuple, OpDelete, pos, fn)
	}
	return nil
}

func (s *postgresCDCSource) emit(relID uint32, tuple *pglogrepl.TupleData, op ChangeOp, pos string, fn func(ChangeEvent) error) error {
	rel, ok := s.relations[relID]
	if !ok || tuple == nil {
		return nil
	}
	values := map[string]string{}
	nulls := map[string]bool{}
	for i, col := range tuple.Columns {
		if i >= len(rel.Columns) {
			break
		}
		name := rel.Columns[i].Name
		switch col.DataType {
		case 'n':
			nulls[name] = true
		case 't':
			values[name] = string(col.Data)
		case 'u':
			// unchanged toast: not present in this message; skip
		}
	}
	key := map[string]string{}
	if pkSet := s.keyCols[rel.RelationName]; len(pkSet) > 0 {
		for name := range pkSet {
			if v, ok := values[name]; ok {
				key[name] = v
			}
		}
	} else {
		for i, rc := range rel.Columns {
			if rc.Flags == 1 && i < len(tuple.Columns) && tuple.Columns[i].DataType == 't' {
				key[rc.Name] = string(tuple.Columns[i].Data)
			}
		}
	}
	return fn(ChangeEvent{
		Op:       op,
		Table:    rel.RelationName,
		Key:      key,
		Row:      importflow.Record{Table: rel.RelationName, Values: values, Nulls: nulls},
		Position: pos,
	})
}
