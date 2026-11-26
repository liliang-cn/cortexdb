package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The tool commands reach the tools through the very server `serve` runs,
// connected in memory: the same list, the same schemas, the same handlers
// and, on a shared brain, the same authorization of the same token. There is
// no second path to a tool for the CLI to drift from.

// toolSession is an MCP client session on the server serve would run.
type toolSession struct {
	*mcp.ClientSession
	close func()
}

func openToolSession(ctx context.Context) (*toolSession, error) {
	var server *mcp.Server
	var release func()
	if remote := strings.TrimSpace(os.Getenv("CORTEXDB_REMOTE")); remote != "" {
		conn, err := dialCortexDB(remote, os.Getenv("CORTEXDB_GRPC_TOKEN"))
		if err != nil {
			return nil, err
		}
		server, err = newRemoteMCPServer(ctx, rpcv1.NewToolsServiceClient(conn), remote)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		release = func() { _ = conn.Close() }
	} else {
		path := localDBPath()
		db, err := openBrainDB(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		server = newLocalMCPServer(db, path)
		release = func() { _ = db.Close() }
	}
	return connectToolSession(ctx, server, release)
}

// connectToolSession connects an in-memory MCP client to server; closing the
// session also calls release.
func connectToolSession(ctx context.Context, server *mcp.Server, release func()) (*toolSession, error) {
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		release()
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: ProgCortexDB, Version: cortexdbroot.Version}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = ss.Close()
		release()
		return nil, err
	}
	return &toolSession{ClientSession: cs, close: func() {
		_ = cs.Close()
		_ = ss.Close()
		release()
	}}, nil
}

func (s *toolSession) tools(ctx context.Context) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	for tool, err := range s.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *toolSession) tool(ctx context.Context, name string) (*mcp.Tool, error) {
	tools, err := s.tools(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Name == name {
			return t, nil
		}
		names = append(names, t.Name)
	}
	return nil, usageErr{fmt.Sprintf("no tool %q%s", name, suggest(name, names))}
}

// usageErr is a mistake on the command line rather than a failure of the
// command: it exits 2.
type usageErr struct{ msg string }

func (e usageErr) Error() string { return e.msg }

// withTools runs fn on a tool session, interruptible with Ctrl-C, and turns
// its error into the exit code.
func withTools(inv *invocation, cmd string, fn func(ctx context.Context, s *toolSession) error) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := openToolSession(ctx)
	if err != nil {
		fmt.Fprintf(inv.stderr, "%s %s: %v\n", inv.prog, cmd, err)
		return exitFail
	}
	defer s.close()
	if err := fn(ctx, s); err != nil {
		fmt.Fprintf(inv.stderr, "%s %s: %v\n", inv.prog, cmd, err)
		if _, ok := err.(usageErr); ok {
			return exitUsage
		}
		return exitFail
	}
	return exitOK
}

// effect names what calling a tool can do to the brain, from its MCP
// annotations.
func effect(t *mcp.Tool) string {
	a := t.Annotations
	switch {
	case a == nil:
		return "unknown"
	case a.ReadOnlyHint:
		return "read-only"
	case a.DestructiveHint != nil && *a.DestructiveHint:
		return "destructive"
	case a.IdempotentHint:
		return "idempotent"
	default:
		return "writes"
	}
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	if r := []rune(s); len(r) > 100 {
		s = string(r[:99]) + "…"
	}
	return s
}

type toolInfo struct {
	Name        string               `json:"name"`
	Effect      string               `json:"effect"`
	Description string               `json:"description"`
	InputSchema any                  `json:"inputSchema,omitempty"`
	Annotations *mcp.ToolAnnotations `json:"annotations,omitempty"`
}

func runToolList(inv *invocation, args []string) int {
	readOnly := false
	for _, a := range args {
		if a == "--read-only" {
			readOnly = true
		}
	}
	return withTools(inv, "tool list", func(ctx context.Context, s *toolSession) error {
		tools, err := s.tools(ctx)
		if err != nil {
			return err
		}
		var infos []toolInfo
		for _, t := range tools {
			if readOnly && effect(t) != "read-only" {
				continue
			}
			infos = append(infos, toolInfo{Name: t.Name, Effect: effect(t), Description: t.Description, Annotations: t.Annotations})
		}
		if inv.json() {
			return writeJSON(inv.stdout, infos)
		}
		width := 0
		for _, i := range infos {
			width = max(width, len(i.Name))
		}
		for _, i := range infos {
			fmt.Fprintf(inv.stdout, "%-*s  %-11s  %s\n", width, i.Name, i.Effect, firstSentence(i.Description))
		}
		return nil
	})
}

// schemaProp is the part of a JSON schema property the CLI reads.
type schemaProp struct {
	Type        any         `json:"type"`
	Description string      `json:"description"`
	Enum        []any       `json:"enum"`
	Items       *schemaProp `json:"items"`
}

type objectSchema struct {
	Properties map[string]schemaProp `json:"properties"`
	Required   []string              `json:"required"`
}

func toolSchema(t *mcp.Tool) objectSchema {
	var s objectSchema
	if raw, err := json.Marshal(t.InputSchema); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// typeName is the property's type, the first non-null one when it is a list.
func (p schemaProp) typeName() string {
	switch v := p.Type.(type) {
	case string:
		return v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

func runToolDescribe(inv *invocation, args []string) int {
	name := firstPositional(inv.cmd, args)
	return withTools(inv, "tool describe", func(ctx context.Context, s *toolSession) error {
		t, err := s.tool(ctx, name)
		if err != nil {
			return err
		}
		if inv.json() {
			return writeJSON(inv.stdout, toolInfo{Name: t.Name, Effect: effect(t), Description: t.Description, InputSchema: t.InputSchema, Annotations: t.Annotations})
		}
		fmt.Fprintf(inv.stdout, "%s (%s)\n\n%s\n", t.Name, effect(t), strings.TrimSpace(t.Description))
		schema := toolSchema(t)
		if len(schema.Properties) == 0 {
			fmt.Fprintln(inv.stdout, "\nNo arguments.")
			return nil
		}
		required := map[string]bool{}
		for _, r := range schema.Required {
			required[r] = true
		}
		keys := make([]string, 0, len(schema.Properties))
		for k := range schema.Properties {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if required[keys[i]] != required[keys[j]] {
				return required[keys[i]]
			}
			return keys[i] < keys[j]
		})
		fmt.Fprintln(inv.stdout, "\nArguments:")
		for _, k := range keys {
			p := schema.Properties[k]
			typ := p.typeName()
			if typ == "array" && p.Items != nil && p.Items.typeName() != "" {
				typ = p.Items.typeName() + "[]"
			}
			if len(p.Enum) > 0 {
				var vals []string
				for _, e := range p.Enum {
					vals = append(vals, fmt.Sprint(e))
				}
				typ = strings.Join(vals, "|")
			}
			req := ""
			if required[k] {
				req = " (required)"
			}
			fmt.Fprintf(inv.stdout, "  %s %s%s\n", k, typ, req)
			if d := strings.TrimSpace(p.Description); d != "" {
				fmt.Fprintf(inv.stdout, "      %s\n", d)
			}
		}
		return nil
	})
}

func runToolCall(inv *invocation, args []string) int {
	name := firstPositional(inv.cmd, args)
	vals, bools := parsedFlags(inv.cmd, args)
	return withTools(inv, "tool call", func(ctx context.Context, s *toolSession) error {
		t, err := s.tool(ctx, name)
		if err != nil {
			return err
		}
		callArgs := map[string]any{}
		for _, raw := range vals["--json"] {
			if raw == "-" {
				b, err := io.ReadAll(inv.stdin)
				if err != nil {
					return err
				}
				raw = string(b)
			}
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.UseNumber()
			var obj map[string]any
			if err := dec.Decode(&obj); err != nil {
				return usageErr{fmt.Sprintf("--json is not a JSON object: %v", err)}
			}
			for k, v := range obj {
				callArgs[k] = v
			}
		}
		if err := applyArgFlags(t, callArgs, vals["--arg"]); err != nil {
			return err
		}
		return callAndPrint(ctx, inv, s, t, callArgs, bools["--yes"])
	})
}

// applyArgFlags adds --arg key=value pairs, each value typed by the tool's
// schema: a string stays a string, a number, boolean or object is parsed,
// and an array grows by one element per --arg (or takes a JSON array).
func applyArgFlags(t *mcp.Tool, callArgs map[string]any, pairs []string) error {
	schema := toolSchema(t)
	names := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		names = append(names, k)
	}
	setByArg := map[string]bool{}
	for _, pair := range pairs {
		key, raw, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return usageErr{fmt.Sprintf("--arg %q is not KEY=VALUE", pair)}
		}
		prop, known := schema.Properties[key]
		if !known && len(schema.Properties) > 0 {
			return usageErr{fmt.Sprintf("%s takes no argument %q%s", t.Name, key, suggest(key, names))}
		}
		if prop.typeName() == "array" {
			if strings.HasPrefix(strings.TrimSpace(raw), "[") {
				v, err := typedValue(key, "array", raw)
				if err != nil {
					return err
				}
				callArgs[key] = v
				setByArg[key] = true
				continue
			}
			itemType := ""
			if prop.Items != nil {
				itemType = prop.Items.typeName()
			}
			v, err := typedValue(key, itemType, raw)
			if err != nil {
				return err
			}
			list, _ := callArgs[key].([]any)
			if !setByArg[key] {
				list = nil
			}
			callArgs[key] = append(list, v)
			setByArg[key] = true
			continue
		}
		v, err := typedValue(key, prop.typeName(), raw)
		if err != nil {
			return err
		}
		callArgs[key] = v
		setByArg[key] = true
	}
	for _, r := range schema.Required {
		if _, ok := callArgs[r]; !ok {
			return usageErr{fmt.Sprintf("%s needs %s (--arg %s=...)", t.Name, r, r)}
		}
	}
	return nil
}

func typedValue(key, typ, raw string) (any, error) {
	switch typ {
	case "string":
		return raw, nil
	case "boolean":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, usageErr{fmt.Sprintf("%s wants true or false, not %q", key, raw)}
		}
		return b, nil
	case "integer":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, usageErr{fmt.Sprintf("%s wants an integer, not %q", key, raw)}
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, usageErr{fmt.Sprintf("%s wants a number, not %q", key, raw)}
		}
		return f, nil
	case "object", "array":
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, usageErr{fmt.Sprintf("%s wants JSON (%s): %v", key, typ, err)}
		}
		return v, nil
	default:
		// No type in the schema: JSON when it reads as JSON, a string
		// otherwise.
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err == nil {
			return v, nil
		}
		return raw, nil
	}
}

// callAndPrint calls a tool and prints its result. A destructive tool is
// called only with --yes or after a yes at the terminal; without a terminal
// to ask, it is refused, so a script never deletes by accident.
func callAndPrint(ctx context.Context, inv *invocation, s *toolSession, t *mcp.Tool, callArgs map[string]any, yes bool) error {
	if effect(t) == "destructive" && !yes {
		if err := confirm(inv, fmt.Sprintf("%s can delete or overwrite data", t.Name)); err != nil {
			return err
		}
	}
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: t.Name, Arguments: callArgs})
	if err != nil {
		return err
	}
	return printResult(inv, res)
}

func confirm(inv *invocation, what string) error {
	if !inv.interactive {
		return usageErr{what + ": pass --yes"}
	}
	fmt.Fprintf(inv.stderr, "%s. Go ahead? [y/N] ", what)
	line, _ := bufio.NewReader(inv.stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
		return nil
	}
	return fmt.Errorf("not called")
}

// printResult writes a tool result. --format json prints one JSON value: the
// structured result when the tool gives one, else the text it returned when
// that is JSON, else {"text": ...}. Text prints JSON indented and anything
// else as it came. A tool error goes to stderr and exits 1.
func printResult(inv *invocation, res *mcp.CallToolResult) error {
	var texts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	if res.IsError {
		return fmt.Errorf("%s", strings.TrimSpace(strings.Join(texts, "\n")))
	}
	if inv.json() {
		switch {
		case res.StructuredContent != nil:
			return writeJSON(inv.stdout, res.StructuredContent)
		case len(texts) == 1 && json.Valid([]byte(texts[0])):
			var buf bytes.Buffer
			if err := json.Compact(&buf, []byte(texts[0])); err != nil {
				return err
			}
			buf.WriteByte('\n')
			_, err := inv.stdout.Write(buf.Bytes())
			return err
		default:
			return writeJSON(inv.stdout, map[string]any{"text": strings.Join(texts, "\n")})
		}
	}
	if len(texts) == 0 && res.StructuredContent != nil {
		return writeIndented(inv.stdout, res.StructuredContent)
	}
	for _, text := range texts {
		var buf bytes.Buffer
		if json.Valid([]byte(text)) && json.Indent(&buf, []byte(text), "", "  ") == nil {
			buf.WriteByte('\n')
			if _, err := inv.stdout.Write(buf.Bytes()); err != nil {
				return err
			}
			continue
		}
		fmt.Fprintln(inv.stdout, strings.TrimRight(text, "\n"))
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

func writeIndented(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// parsedFlags reads arguments already held to the command's flag table by
// checkArgs: the values of each flag in order, and the boolean flags given.
func parsedFlags(cmd *command, args []string) (map[string][]string, map[string]bool) {
	vals := map[string][]string{}
	bools := map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		if name == "-y" {
			name = "--yes"
		}
		switch {
		case hasValue:
			vals[name] = append(vals[name], value)
		case flagTakesValue(cmd, name):
			if i+1 < len(args) {
				i++
				vals[name] = append(vals[name], args[i])
			}
		default:
			bools[name] = true
		}
	}
	return vals, bools
}

func flagTakesValue(cmd *command, name string) bool {
	f, ok := cmd.flag(name)
	return ok && f.value != ""
}

// positionals are the arguments that are not flags or flag values.
func positionals(cmd *command, args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i+1:]...)
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			out = append(out, a)
			continue
		}
		if _, _, hasValue := strings.Cut(a, "="); !hasValue && flagTakesValue(cmd, a) {
			i++
		}
	}
	return out
}

func firstPositional(cmd *command, args []string) string {
	if p := positionals(cmd, args); len(p) > 0 {
		return p[0]
	}
	return ""
}

// The shortcuts: the three calls people make most, without spelling the
// tool's arguments.

func runSearch(inv *invocation, args []string) int {
	vals, _ := parsedFlags(inv.cmd, args)
	callArgs := map[string]any{"query": strings.Join(positionals(inv.cmd, args), " ")}
	if v := vals["--top-k"]; len(v) > 0 {
		n, err := strconv.Atoi(v[len(v)-1])
		if err != nil || n <= 0 {
			return inv.usageError(inv.cmd, fmt.Sprintf("--top-k wants a positive integer, not %q", v[len(v)-1]))
		}
		callArgs["top_k_memories"] = n
		callArgs["top_k_knowledge"] = n
	}
	if v := vals["--retrieval-mode"]; len(v) > 0 {
		callArgs["retrieval_mode"] = v[len(v)-1]
	}
	return withTools(inv, "search", func(ctx context.Context, s *toolSession) error {
		t, err := s.tool(ctx, "knowledge_memory_recall")
		if err != nil {
			return err
		}
		if inv.json() {
			return callAndPrint(ctx, inv, s, t, callArgs, false)
		}
		res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: t.Name, Arguments: callArgs})
		if err != nil {
			return err
		}
		if !res.IsError && len(res.Content) == 1 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				if printed, err := printRecall(inv.stdout, tc.Text); printed || err != nil {
					return err
				}
			}
		}
		return printResult(inv, res)
	})
}

func runRemember(inv *invocation, args []string) int {
	vals, bools := parsedFlags(inv.cmd, args)
	content := strings.Join(positionals(inv.cmd, args), " ")
	id := ""
	if v := vals["--id"]; len(v) > 0 {
		id = v[len(v)-1]
	}
	callArgs := map[string]any{"content": content}
	if v := vals["--scope"]; len(v) > 0 {
		callArgs["scope"] = v[len(v)-1]
	}
	return withTools(inv, "remember", func(ctx context.Context, s *toolSession) error {
		t, err := s.tool(ctx, "memory_save")
		if err != nil {
			return err
		}
		// memory_save replaces the memory an id names, which is why it is
		// destructive; under a fresh id it replaces nothing, and only an id
		// that is already stored needs the yes.
		yes := bools["--yes"]
		if id == "" {
			id = newMemoryID()
			yes = true
		} else if !yes {
			exists, err := memoryExists(ctx, s, id)
			if err != nil {
				return err
			}
			yes = !exists
		}
		callArgs["memory_id"] = id
		if !yes {
			if err := confirm(inv, fmt.Sprintf("memory %s exists and will be replaced", id)); err != nil {
				return err
			}
		}
		return callAndPrint(ctx, inv, s, t, callArgs, true)
	})
}

func runGet(inv *invocation, args []string) int {
	ids := positionals(inv.cmd, args)
	callArgs := map[string]any{"memory_id": ids[0]}
	if len(ids) > 1 {
		callArgs = map[string]any{"memory_ids": ids}
	}
	return withTools(inv, "get", func(ctx context.Context, s *toolSession) error {
		t, err := s.tool(ctx, "memory_get")
		if err != nil {
			return err
		}
		return callAndPrint(ctx, inv, s, t, callArgs, false)
	})
}

// memoryExists asks memory_get in its batch form, which lists an id it does
// not hold under missing rather than failing the call.
func memoryExists(ctx context.Context, s *toolSession, id string) (bool, error) {
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "memory_get", Arguments: map[string]any{"memory_ids": []string{id}}})
	if err != nil {
		return false, err
	}
	if res.IsError {
		return false, printResult(&invocation{format: "text", stdout: io.Discard}, res)
	}
	var got struct {
		Missing []string `json:"missing"`
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &got); err != nil {
				return false, fmt.Errorf("memory_get: %v", err)
			}
		}
	}
	for _, m := range got.Missing {
		if m == id {
			return false, nil
		}
	}
	return true, nil
}

func newMemoryID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "cli-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}
