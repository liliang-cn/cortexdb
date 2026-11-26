package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

func runCLI(t *testing.T, prog string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	inv := &invocation{prog: prog, format: "text", stdin: strings.NewReader(""), stdout: &out, stderr: &errb}
	code = run(inv, args)
	return code, out.String(), errb.String()
}

// useLocalBrain points the CLI at a fresh file and away from any brain the
// environment names.
func useLocalBrain(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brain.db")
	t.Setenv("CORTEXDB_PATH", path)
	t.Setenv("CORTEXDB_REMOTE", "")
	t.Setenv("CORTEXDB_GRPC_TOKEN", "")
	return path
}

func TestHelpPrintsUsageAndExitsZero(t *testing.T) {
	cases := []struct {
		prog string
		args []string
	}{
		{ProgCortexDB, nil},
		{ProgCortexDB, []string{"--help"}},
		{ProgCortexDB, []string{"-h"}},
		{ProgCortexDB, []string{"help"}},
		{ProgCortexDB, []string{"graph"}},
		{ProgCortexDB, []string{"graph", "--help"}},
		{ProgCortexDB, []string{"graph", "html", "--help"}},
		{ProgCortexDB, []string{"--help", "graph", "html"}},
		{ProgCortexDB, []string{"help", "tool", "call"}},
		{ProgCortexDB, []string{"tool", "call", "x", "--help"}},
		{ProgStdio, []string{"--help"}},
		{ProgStdio, []string{"--graph-html", "--help"}},
		{ProgStdio, []string{"help", "--recall"}},
	}
	for _, c := range cases {
		code, out, errOut := runCLI(t, c.prog, c.args...)
		if code != exitOK || !strings.Contains(out, "Usage:") {
			t.Errorf("%s %v: exit %d, stdout %q, stderr %q", c.prog, c.args, code, out, errOut)
		}
	}
}

// A mistyped command line used to fall through to the MCP server, which
// exits 0 when stdin closes: a typo in a script read as success.
func TestAWrongCommandLineExitsTwo(t *testing.T) {
	cases := []struct {
		prog string
		args []string
		want string
	}{
		{ProgCortexDB, []string{"--bogus"}, `unknown flag "--bogus"`},
		{ProgStdio, []string{"--bogus"}, `unknown flag "--bogus"`},
		{ProgStdio, []string{"--recal"}, "did you mean --recall?"},
		{ProgCortexDB, []string{"serch", "x"}, "did you mean search?"},
		{ProgCortexDB, []string{"graph", "htm"}, "did you mean graph html?"},
		{ProgCortexDB, []string{"graph", "html", "--organise"}, "did you mean --organize?"},
		{ProgCortexDB, []string{"graph", "html", "a", "b"}, `unexpected argument "b"`},
		{ProgCortexDB, []string{"tool", "describe"}, "missing <tool>"},
		{ProgCortexDB, []string{"doctor", "--self-test=yes"}, "takes no value"},
		{ProgCortexDB, []string{"memory", "export", "--min-grade"}, "needs a value"},
		{ProgCortexDB, []string{"--format", "yaml", "version"}, "text or json"},
		{ProgCortexDB, []string{"graph", "html", "--format", "json"}, "no JSON output"},
		{ProgCortexDB, []string{"--db", "a.db", "--remote", "h:1", "version"}, "two brains"},
		{ProgCortexDB, []string{"completion", "tcsh"}, "bash, zsh and fish"},
	}
	for _, c := range cases {
		code, _, errOut := runCLI(t, c.prog, c.args...)
		if code != exitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%s %v: exit %d, stderr %q, want exit 2 with %q", c.prog, c.args, code, errOut, c.want)
		}
	}
}

func TestVersion(t *testing.T) {
	_, out, _ := runCLI(t, ProgStdio, "--version")
	if !strings.HasPrefix(out, "cortexdb-mcp-stdio v") {
		t.Fatalf("--version printed %q", out)
	}
	_, out, _ = runCLI(t, ProgCortexDB, "version", "--format", "json")
	var v struct{ Name, Version string }
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Name != "cortexdb" || v.Version == "" {
		t.Fatalf("version --format json printed %q", out)
	}
}

// Every mode keeps its cortexdb-mcp-stdio spelling, and it is the same
// command as the cortexdb one: same flags, same help.
func TestEveryLegacyModeIsItsCommand(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands {
		if c.legacy == "" {
			continue
		}
		if seen[c.legacy] {
			t.Errorf("%s is the legacy spelling of two commands", c.legacy)
		}
		seen[c.legacy] = true
		if got := lookupLegacy(c.legacy); got != c {
			t.Errorf("%s resolves to %v, want %s", c.legacy, got, c.name())
		}
		if got, rest := lookupCommand(c.path); got != c || len(rest) != 0 {
			t.Errorf("%s does not resolve to itself", c.name())
		}
		for _, prog := range []string{ProgStdio, ProgCortexDB} {
			code, out, _ := runCLI(t, prog, c.legacy, "--help")
			if code != exitOK || !strings.Contains(out, c.legacy) {
				t.Errorf("%s %s --help: exit %d, %q", prog, c.legacy, code, out)
			}
		}
		for _, f := range c.flags {
			args := []string{f.name}
			if f.value != "" {
				args = append(args, "v")
			}
			if _, _, err := checkArgs(c, args); err != nil {
				t.Errorf("%s %s: %v", c.legacy, f.name, err)
			}
		}
	}
	// The modes main.go dispatched before the table existed.
	for _, legacy := range []string{"--capture-session", "--doctor", "--export-memory", "--facts-as-of", "--file-context",
		"--global-search", "--graph-3d", "--graph-cleanup", "--graph-html", "--graph-update", "--import-agent-memory",
		"--import-code-graph", "--import-learning-graph", "--learn-mastered", "--learn-next", "--learn-path",
		"--memory-html", "--memory-usage", "--multi-hop", "--recall-eval", "--recall", "--reembed-memories",
		"--resolve-entities", "--session-context", "--sweep-expired", "--sync-memory", "--version", "-version"} {
		if lookupLegacy(legacy) == nil {
			t.Errorf("%s is no longer accepted", legacy)
		}
	}
}

// invocationRE finds a cortexdb-mcp-stdio invocation in the repo's plugins,
// hooks and docs: the binary (or the variable holding it), a mode, and the
// rest of the command up to a comment, pipe or the end of the code span.
var invocationRE = regexp.MustCompile(`(?:cortexdb-mcp(?:-stdio)?(?:\.exe)?|\$\{?bin\}?|\$\{?CORTEXDB_MCP_BIN\}?)"?[ \t]+(--?[a-z][a-z0-9-]*)([^\n|#;&` + "`" + `]*)`)

var flagTokenRE = regexp.MustCompile(`(?:^|[\s\[])(--?[a-z][a-z0-9-]*)`)

func TestEveryInvocationInTheRepoStillParses(t *testing.T) {
	root := filepath.Join("..", "..")
	var dirs = []string{"plugins", "docs", "README.md", "README_CN.md", "SKILL.md", "examples", "clients"}
	checked := 0
	for _, d := range dirs {
		_ = filepath.Walk(filepath.Join(root, d), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				switch info.Name() {
				case "node_modules", ".git", "dist", "target":
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(path) {
			case ".go", ".png", ".jpg", ".gif", ".svg", ".lock", ".wasm", ".mp4":
				return nil
			}
			if info.Size() > 2<<20 {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, m := range invocationRE.FindAllStringSubmatch(string(raw), -1) {
				mode, rest := m[1], m[2]
				if _, ok := globalFlag(mode); ok {
					continue
				}
				cmd := lookupLegacy(mode)
				if cmd == nil {
					t.Errorf("%s: %q is not a mode cortexdb-mcp-stdio accepts", path, m[0])
					continue
				}
				checked++
				for _, f := range flagTokenRE.FindAllStringSubmatch(rest, -1) {
					name := f[1]
					if lookupLegacy(name) != nil {
						break // prose naming another mode
					}
					if _, ok := cmd.flag(name); ok {
						continue
					}
					if _, ok := globalFlag(name); ok {
						continue
					}
					t.Errorf("%s: %q passes %s, which %s does not take", path, strings.TrimSpace(m[0]), name, cmd.legacy)
				}
			}
			return nil
		})
	}
	t.Logf("checked %d invocations", checked)
	if checked < 20 {
		t.Fatalf("found only %d invocations; the pattern no longer matches how the repo calls the binary", checked)
	}
}

// The hooks run with exactly these command lines.
func TestTheHookCommandLinesParse(t *testing.T) {
	for _, args := range [][]string{
		{"--recall"}, {"--session-context"}, {"--file-context"}, {"--capture-session"},
		{"--doctor", "--self-test"}, {"--sweep-expired", "--dry-run", "--json"},
		{"--import-agent-memory", "--sessions", "--since", "30d", "--max", "5", "--dry-run"},
		{"--graph-3d", "--no-open", "--listen", "127.0.0.1:47823", "--token-file", "/x", "--interval", "2s"},
		{"--graph-update", "-", "-n", "--allow-delete"},
		{"--facts-as-of", "2022-06-01T00:00:00Z", "--from", "Alice", "--type", "works_at"},
		{"--export-memory", "./out", "--min-grade=verified"},
		{"--resolve-entities", "--types=Person,Org", "--merge-all"},
	} {
		cmd := lookupLegacy(args[0])
		if cmd == nil {
			t.Fatalf("%s: no such mode", args[0])
		}
		pos, help, err := checkArgs(cmd, args[1:])
		if err != nil || help {
			t.Errorf("%v: %v (help %v)", args, err, help)
		}
		if cmd.maxArgs >= 0 && len(pos) > cmd.maxArgs {
			t.Errorf("%v: %d positional arguments, at most %d", args, len(pos), cmd.maxArgs)
		}
	}
}

func TestCommonFlagsSetTheBrainThroughTheEnvironment(t *testing.T) {
	t.Setenv("CORTEXDB_PATH", "")
	t.Setenv("CORTEXDB_REMOTE", "remote.example:1")
	t.Setenv("CORTEXDB_GRPC_TOKEN", "")
	inv := &invocation{format: "text"}
	rest, _, err := inv.takeGlobals([]string{"tool", "--db", "x.db", "list", "--format=json", "--", "--db"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rest, " "); got != "tool list -- --db" {
		t.Fatalf("rest %q", got)
	}
	if os.Getenv("CORTEXDB_PATH") != "x.db" || os.Getenv("CORTEXDB_REMOTE") != "" || !inv.json() {
		t.Fatalf("--db did not win over CORTEXDB_REMOTE, or --format was lost")
	}

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv = &invocation{format: "text"}
	if _, _, err := inv.takeGlobals([]string{"--remote", "h:2", "--remote-token-file", tokenFile}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CORTEXDB_REMOTE") != "h:2" || os.Getenv("CORTEXDB_GRPC_TOKEN") != "s3cret" {
		t.Fatalf("--remote / --remote-token-file not applied")
	}
}

func listedNames(t *testing.T, out string) []string {
	t.Helper()
	var infos []toolInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatalf("tool list --format json is not JSON: %v\n%s", err, out)
	}
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name
	}
	return names
}

func serverNames(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	var names []string
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// tool list is the list serve exposes: the CLI reaches the tools through
// that server, so no tool can be callable from an agent and not a shell.
func TestToolListIsWhatServeExposes(t *testing.T) {
	path := useLocalBrain(t)
	code, out, errOut := runCLI(t, ProgCortexDB, "tool", "list", "--format", "json")
	if code != exitOK {
		t.Fatalf("tool list: exit %d: %s", code, errOut)
	}
	got := listedNames(t, out)

	db, err := openBrainDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	want := serverNames(t, newLocalMCPServer(db, path))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tool list has %d tools, serve has %d", len(got), len(want))
	}
	if len(got) < 50 {
		t.Fatalf("only %d tools listed", len(got))
	}

	code, out, _ = runCLI(t, ProgCortexDB, "tool", "list", "--read-only", "--format", "json")
	var infos []toolInfo
	_ = json.Unmarshal([]byte(out), &infos)
	if code != exitOK || len(infos) == 0 || len(infos) >= len(got) {
		t.Fatalf("--read-only listed %d of %d", len(infos), len(got))
	}
	for _, info := range infos {
		if info.Effect != "read-only" {
			t.Fatalf("--read-only listed %s (%s)", info.Name, info.Effect)
		}
	}
}

func TestRememberSearchGetRoundTrip(t *testing.T) {
	useLocalBrain(t)
	code, out, errOut := runCLI(t, ProgCortexDB, "remember", "the", "deploy", "key", "rotates", "weekly", "--format", "json")
	if code != exitOK {
		t.Fatalf("remember: exit %d: %s", code, errOut)
	}
	var saved struct {
		Memory struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(out), &saved); err != nil || saved.Memory.ID == "" {
		t.Fatalf("remember printed %q", out)
	}

	code, out, errOut = runCLI(t, ProgCortexDB, "get", saved.Memory.ID)
	if code != exitOK || !strings.Contains(out, "rotates weekly") {
		t.Fatalf("get: exit %d, %q, %s", code, out, errOut)
	}
	code, out, errOut = runCLI(t, ProgCortexDB, "search", "deploy", "key", "rotation")
	if code != exitOK || !strings.Contains(out, saved.Memory.ID) {
		t.Fatalf("search: exit %d, %q, %s", code, out, errOut)
	}

	// Replacing it is destructive: refused without a terminal or --yes.
	code, _, errOut = runCLI(t, ProgCortexDB, "remember", "--id", saved.Memory.ID, "something else")
	if code != exitUsage || !strings.Contains(errOut, "pass --yes") {
		t.Fatalf("replacing without --yes: exit %d, %s", code, errOut)
	}
	code, _, errOut = runCLI(t, ProgCortexDB, "remember", "--id", saved.Memory.ID, "--yes", "something else")
	if code != exitOK {
		t.Fatalf("replacing with --yes: exit %d, %s", code, errOut)
	}
	_, out, _ = runCLI(t, ProgCortexDB, "get", saved.Memory.ID)
	if !strings.Contains(out, "something else") {
		t.Fatalf("the replacement did not land: %q", out)
	}
}

func TestToolCallArguments(t *testing.T) {
	useLocalBrain(t)
	// A destructive tool is refused without --yes when nobody can be asked.
	code, _, errOut := runCLI(t, ProgCortexDB, "tool", "call", "memory_delete", "--arg", "memory_id=x")
	if code != exitUsage || !strings.Contains(errOut, "pass --yes") {
		t.Fatalf("memory_delete without --yes: exit %d, %s", code, errOut)
	}
	// A tool error is exit 1, on stderr.
	code, _, errOut = runCLI(t, ProgCortexDB, "tool", "call", "memory_get", "--json", `{"memory_id":"nope"}`)
	if code != exitFail || errOut == "" {
		t.Fatalf("memory_get of a missing id: exit %d, %q", code, errOut)
	}
	// A misspelt argument is caught before the call.
	code, _, errOut = runCLI(t, ProgCortexDB, "tool", "call", "memory_get", "--arg", "memry_id=x")
	if code != exitUsage || !strings.Contains(errOut, "did you mean memory_id?") {
		t.Fatalf("misspelt argument: exit %d, %s", code, errOut)
	}
	code, _, errOut = runCLI(t, ProgCortexDB, "tool", "call", "no_such_tool")
	if code != exitUsage || !strings.Contains(errOut, `no tool "no_such_tool"`) {
		t.Fatalf("unknown tool: exit %d, %s", code, errOut)
	}
	// --json from stdin, a value that reads "--help" kept as a value.
	var out, errb bytes.Buffer
	inv := &invocation{prog: ProgCortexDB, format: "text", stdin: strings.NewReader(`{"memory_id":"m1","scope":"global","content":"--help"}`), stdout: &out, stderr: &errb}
	if code := run(inv, []string{"tool", "call", "memory_save", "--json", "-", "--yes"}); code != exitOK {
		t.Fatalf("memory_save from stdin: exit %d, %s", code, errb.String())
	}
	_, got, _ := runCLI(t, ProgCortexDB, "get", "m1")
	if !strings.Contains(got, `"--help"`) {
		t.Fatalf("stdin JSON did not land: %q", got)
	}
}

func TestArgFlagsAreTypedByTheSchema(t *testing.T) {
	tool := &mcp.Tool{Name: "t", InputSchema: map[string]any{
		"type":     "object",
		"required": []any{"q"},
		"properties": map[string]any{
			"q":     map[string]any{"type": "string"},
			"n":     map[string]any{"type": "integer"},
			"f":     map[string]any{"type": "number"},
			"b":     map[string]any{"type": "boolean"},
			"tags":  map[string]any{"type": []any{"null", "array"}, "items": map[string]any{"type": "string"}},
			"ids":   map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			"meta":  map[string]any{"type": "object"},
			"loose": map[string]any{},
		},
	}}
	args := map[string]any{"tags": []any{"from-json"}}
	err := applyArgFlags(tool, args, []string{"q=123", "n=7", "f=0.5", "b=true", "tags=a", "tags=b", "ids=[1,2]", "meta={\"k\":1}", "loose=42"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(args)
	want := `{"b":true,"f":0.5,"ids":[1,2],"loose":42,"meta":{"k":1},"n":7,"q":"123","tags":["a","b"]}`
	if string(raw) != want {
		t.Fatalf("got %s\nwant %s", raw, want)
	}
	for _, bad := range [][]string{{"n=seven", "q=x"}, {"b=maybe", "q=x"}, {"meta=nope", "q=x"}, {"novalue"}, {"n=1"}} {
		if err := applyArgFlags(tool, map[string]any{}, bad); err == nil {
			t.Errorf("%v: accepted", bad)
		} else if _, ok := err.(usageErr); !ok {
			t.Errorf("%v: %v is not a usage error", bad, err)
		}
	}
}

// The tools reach a shared brain through the bridge serve runs, under the
// token's authority: what the brain refuses an agent it refuses the CLI.
func TestToolCallOnASharedBrainKeepsTheKeysRefusals(t *testing.T) {
	keys, err := authz.NewKeySet([]authz.Key{
		{ID: "writer", Secret: "rw", Clearance: authz.ReadWrite},
		{ID: "reader", Secret: "ro", Clearance: authz.ReadOnly},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "shared.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	addr := serveTestBrain(t, rpcserver.New(db, rpcserver.Options{Keys: keys, DBPath: dbPath}))
	t.Cleanup(func() { _ = db.Close() })

	tokenDir := t.TempDir()
	token := func(secret string) string {
		p := filepath.Join(tokenDir, secret)
		if err := os.WriteFile(p, []byte(secret), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Setenv("CORTEXDB_PATH", "")
	t.Setenv("CORTEXDB_REMOTE", "")
	t.Setenv("CORTEXDB_GRPC_TOKEN", "")

	code, out, errOut := runCLI(t, ProgCortexDB, "--remote", addr, "--remote-token-file", token("rw"), "tool", "list", "--format", "json")
	if code != exitOK {
		t.Fatalf("remote tool list: exit %d: %s", code, errOut)
	}
	if names := listedNames(t, out); len(names) < 50 {
		t.Fatalf("remote tool list: %d tools", len(names))
	}
	code, _, errOut = runCLI(t, ProgCortexDB, "--remote", addr, "--remote-token-file", token("rw"), "remember", "--id", "shared-1", "on the shared brain")
	if code != exitOK {
		t.Fatalf("remote remember: exit %d: %s", code, errOut)
	}
	code, out, errOut = runCLI(t, ProgCortexDB, "--remote", addr, "--remote-token-file", token("ro"), "get", "shared-1")
	if code != exitOK || !strings.Contains(out, "on the shared brain") {
		t.Fatalf("read-only key get: exit %d, %q, %s", code, out, errOut)
	}
	code, _, errOut = runCLI(t, ProgCortexDB, "--remote", addr, "--remote-token-file", token("ro"), "remember", "a read-only key writes nothing")
	if code != exitFail || errOut == "" {
		t.Fatalf("read-only key remember: exit %d, %s; the brain should refuse it", code, errOut)
	}
}

func serveTestBrain(t *testing.T, srv *grpc.Server) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestCompletion(t *testing.T) {
	names := func() []string { return []string{"memory_get", "memory_save"} }
	args := func(string) []string { return []string{"memory_id", "memory_ids"} }
	cases := []struct {
		words []string
		want  string
	}{
		{[]string{"gr"}, "graph "},
		{[]string{"graph", ""}, "html |3d |update |cleanup |resolve-entities "},
		{[]string{"graph", "html", "--o"}, "--organize "},
		{[]string{"--db", "x.db", "graph", "html", "--o"}, "--organize "},
		{[]string{"tool", "call", "memory_s"}, "memory_save "},
		{[]string{"tool", "call", "memory_get", "--arg", "memory_i"}, "memory_id=|memory_ids="},
		{[]string{"--format", "j"}, "json "},
		{[]string{"remember", "--scope", ""}, "global |user |session "},
		{[]string{"completion", "z"}, "zsh "},
		{[]string{"graph", "html", "--dry"}, ""},
	}
	for _, c := range cases {
		if got := strings.Join(completeWords(c.words, names, args), "|"); got != c.want {
			t.Errorf("%q: got %q, want %q", c.words, got, c.want)
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out, _ := runCLI(t, ProgCortexDB, "completion", shell)
		if code != exitOK || !strings.Contains(out, "cortexdb __complete") {
			t.Errorf("completion %s: exit %d, %q", shell, code, out)
		}
	}
}
