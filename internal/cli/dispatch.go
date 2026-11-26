package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"golang.org/x/term"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
)

// Exit codes: 0 done, 1 the command failed, 2 the command line was wrong.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// ProgCortexDB and ProgStdio are the two binaries built from this package.
// They take the same commands; they differ in what no arguments means and in
// the spelling their help shows.
const (
	ProgCortexDB = "cortexdb"
	ProgStdio    = "cortexdb-mcp-stdio"
)

// invocation is what the common flags decided for one run.
type invocation struct {
	prog   string
	format string
	quiet  bool
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	cmd    *command
	// interactive: stdin is a terminal, so a destructive call can ask.
	interactive bool
}

func (inv *invocation) json() bool { return inv.format == "json" }

// globalFlags are taken by every command, anywhere before `--`.
var globalFlags = []flagSpec{
	{name: "--db", value: "PATH", help: "local brain file (overrides CORTEXDB_PATH and CORTEXDB_REMOTE)"},
	{name: "--remote", value: "HOST:PORT", help: "shared brain served by cortexdb-grpc (overrides CORTEXDB_REMOTE)"},
	{name: "--remote-token-file", value: "PATH", help: "file holding the shared brain's token (overrides CORTEXDB_GRPC_TOKEN)"},
	{name: "--format", value: "text|json", help: "output for people (text, the default) or for programs (json)"},
	{name: "--quiet", alias: "-q", help: "drop log lines (progress, warnings); errors still print"},
	{name: "--help", alias: "-h", help: "show help"},
}

func globalFlag(name string) (flagSpec, bool) {
	for _, f := range globalFlags {
		if f.name == name || f.alias == name {
			return f, true
		}
	}
	return flagSpec{}, false
}

// Main runs one command line and returns the process's exit code.
func Main(prog string, args []string) int {
	inv := &invocation{prog: prog, format: "text", stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
	// A terminal on both ends: /dev/null is a character device too, and a
	// question asked of it would read as no.
	inv.interactive = term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	return run(inv, args)
}

func run(inv *invocation, args []string) int {
	if len(args) > 0 && args[0] == "__complete" {
		words := args[1:]
		if len(words) > 1 {
			// The brain flags among the finished words pick the brain tool
			// names come from; a half-typed one is no error here.
			_, _, _ = inv.takeGlobals(words[:len(words)-1])
		}
		return runComplete(inv, words)
	}
	rest, help, err := inv.takeGlobals(args)
	if err != nil {
		return inv.usageError(nil, err.Error())
	}

	if len(rest) == 0 {
		if help || inv.prog != ProgStdio {
			inv.printTopHelp(inv.stdout)
			return exitOK
		}
		return serve()
	}

	var cmd *command
	var cmdArgs []string
	if strings.HasPrefix(rest[0], "-") && rest[0] != "-" {
		cmd = lookupLegacy(rest[0])
		if cmd == nil {
			return inv.usageError(nil, fmt.Sprintf("unknown flag %q%s", rest[0], suggest(rest[0], allFirstWords())))
		}
		cmdArgs = rest[1:]
	} else {
		cmd, cmdArgs = lookupCommand(rest)
		if cmd == nil {
			if subs := subcommandsOf(rest[0]); len(subs) > 0 {
				if len(rest) == 1 || help || rest[1] == "-h" || rest[1] == "--help" {
					inv.printGroupHelp(inv.stdout, rest[0], subs)
					return exitOK
				}
				return inv.usageError(nil, fmt.Sprintf("unknown command %q%s", rest[0]+" "+rest[1], suggestAs(rest[1], subs)))
			}
			return inv.usageError(nil, fmt.Sprintf("unknown command %q%s", rest[0], suggest(rest[0], allFirstWords())))
		}
	}

	if help {
		inv.printCommandHelp(inv.stdout, cmd)
		return exitOK
	}
	if cmd.anyArgs {
		inv.cmd = cmd
		return cmd.run(inv, cmdArgs)
	}
	positional, cmdHelp, err := checkArgs(cmd, cmdArgs)
	if cmdHelp {
		inv.printCommandHelp(inv.stdout, cmd)
		return exitOK
	}
	if err != nil {
		return inv.usageError(cmd, err.Error())
	}
	if len(positional) < cmd.minArgs {
		return inv.usageError(cmd, "missing "+cmd.args)
	}
	if cmd.maxArgs >= 0 && len(positional) > cmd.maxArgs {
		return inv.usageError(cmd, fmt.Sprintf("unexpected argument %q", positional[cmd.maxArgs]))
	}
	if inv.json() {
		if cmd.jsonArgs == nil {
			return inv.usageError(cmd, "has no JSON output (--format json)")
		}
		cmdArgs = cmd.jsonArgs(cmdArgs)
	}
	if inv.quiet {
		log.SetOutput(io.Discard)
	}
	inv.cmd = cmd
	return cmd.run(inv, cmdArgs)
}

// takeGlobals removes the common flags from args, applying them, and reports
// whether help was asked for before the command could be known. The brain
// flags are applied through the environment every mode already reads, so a
// flag wins over the variable it names and reaches every mode the same way.
// A token is read from a file and never taken on the command line, where any
// user of the machine could read it.
func (inv *invocation) takeGlobals(args []string) (rest []string, help bool, err error) {
	var db, remote, tokenFile string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, value, hasValue := strings.Cut(a, "=")
		f, ok := globalFlag(name)
		if !ok {
			rest = append(rest, a)
			continue
		}
		// -h and --help belong to the command once there is one: they are
		// left in place for checkArgs, which knows the command's own
		// values (a --json object may well contain "--help").
		if f.name == "--help" {
			if len(rest) == 0 {
				help = true
			} else {
				rest = append(rest, a)
			}
			continue
		}
		if f.value != "" && !hasValue {
			if i+1 >= len(args) {
				return nil, false, fmt.Errorf("%s needs a value (%s)", f.name, f.value)
			}
			i++
			value = args[i]
		} else if f.value == "" && hasValue {
			return nil, false, fmt.Errorf("%s takes no value", f.name)
		}
		switch f.name {
		case "--db":
			db = value
		case "--remote":
			remote = value
		case "--remote-token-file":
			tokenFile = value
		case "--format":
			if value != "text" && value != "json" {
				return nil, false, fmt.Errorf("--format is text or json, not %q", value)
			}
			inv.format = value
		case "--quiet":
			inv.quiet = true
		}
	}
	if db != "" && remote != "" {
		return nil, false, fmt.Errorf("--db and --remote name two brains: pass one")
	}
	if db != "" {
		_ = os.Setenv("CORTEXDB_PATH", db)
		_ = os.Unsetenv("CORTEXDB_REMOTE")
	}
	if remote != "" {
		_ = os.Setenv("CORTEXDB_REMOTE", remote)
	}
	if tokenFile != "" {
		raw, readErr := os.ReadFile(tokenFile)
		if readErr != nil {
			return nil, false, fmt.Errorf("--remote-token-file: %v", readErr)
		}
		_ = os.Setenv("CORTEXDB_GRPC_TOKEN", strings.TrimSpace(string(raw)))
	}
	return rest, help, nil
}

// checkArgs holds a command's arguments to its flag table and returns the
// positional ones. -h/--help anywhere before `--` asks for the command's
// help; it is found here, after flag values are skipped, so a value that
// happens to read "--help" stays a value.
func checkArgs(cmd *command, args []string) (positional []string, help bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(positional, args[i+1:]...), false, nil
		}
		if a == "-h" || a == "--help" {
			return nil, true, nil
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		name, _, hasValue := strings.Cut(a, "=")
		f, ok := cmd.flag(name)
		if !ok {
			return nil, false, fmt.Errorf("unknown flag %q%s", name, suggest(name, flagNames(cmd)))
		}
		switch {
		case f.value == "" && hasValue:
			return nil, false, fmt.Errorf("%s takes no value", f.name)
		case f.value != "" && !hasValue:
			if i+1 >= len(args) {
				return nil, false, fmt.Errorf("%s needs a value (%s)", f.name, f.value)
			}
			i++
		}
	}
	return positional, false, nil
}

func (inv *invocation) usageError(cmd *command, msg string) int {
	prefix := inv.prog
	hint := inv.prog + " --help"
	if cmd != nil {
		prefix = inv.prog + " " + inv.spell(cmd)
		hint = inv.prog + " " + inv.spell(cmd) + " --help"
	}
	fmt.Fprintf(inv.stderr, "%s: %s\nRun '%s' for usage.\n", prefix, msg, hint)
	return exitUsage
}

// spell is the command as this binary's help writes it: the legacy flag in
// cortexdb-mcp-stdio, so its help reads the way the plugins call it.
func (inv *invocation) spell(cmd *command) string {
	if inv.prog == ProgStdio && cmd.legacy != "" {
		return cmd.legacy
	}
	return cmd.name()
}

func runVersion(inv *invocation, _ []string) int {
	if inv.json() {
		fmt.Fprintf(inv.stdout, "{\"name\":%q,\"version\":%q}\n", inv.prog, cortexdbroot.Version)
		return exitOK
	}
	fmt.Fprintln(inv.stdout, inv.prog+" v"+cortexdbroot.Version)
	return exitOK
}

func runHelp(inv *invocation, args []string) int {
	if len(args) == 0 {
		inv.printTopHelp(inv.stdout)
		return exitOK
	}
	if cmd, rest := lookupCommand(args); cmd != nil && len(rest) == 0 {
		inv.printCommandHelp(inv.stdout, cmd)
		return exitOK
	}
	if cmd := lookupLegacy(args[0]); cmd != nil && len(args) == 1 {
		inv.printCommandHelp(inv.stdout, cmd)
		return exitOK
	}
	if subs := subcommandsOf(args[0]); len(subs) > 0 && len(args) == 1 {
		inv.printGroupHelp(inv.stdout, args[0], subs)
		return exitOK
	}
	return inv.usageError(nil, fmt.Sprintf("no command %q%s", strings.Join(args, " "), suggest(args[len(args)-1], allFirstWords())))
}

func (inv *invocation) printTopHelp(w io.Writer) {
	if inv.prog == ProgStdio {
		fmt.Fprintf(w, "Usage: %s [flags]            serve MCP on stdio\n       %s --<mode> [args]   run one mode and exit\n       %s <command> [args]  the same commands as cortexdb\n\nModes:\n", inv.prog, inv.prog, inv.prog)
		for _, c := range commands {
			if c.legacy == "" {
				continue
			}
			fmt.Fprintf(w, "  %-24s %s (cortexdb %s)\n", c.legacy, c.summary, c.name())
		}
	} else {
		fmt.Fprintf(w, "Usage: %s <command> [args] [flags]\n", inv.prog)
		byGroup := map[string][]*command{}
		for _, c := range commands {
			byGroup[c.group] = append(byGroup[c.group], c)
		}
		for _, g := range groupOrder {
			fmt.Fprintf(w, "\n%s:\n", g)
			for _, c := range byGroup[g] {
				fmt.Fprintf(w, "  %-24s %s\n", c.name(), c.summary)
			}
		}
	}
	fmt.Fprintln(w, "\nFlags for every command:")
	printFlags(w, globalFlags)
	fmt.Fprintf(w, "\nRun '%s help <command>' for a command's arguments and flags.\n", inv.prog)
}

func (inv *invocation) printGroupHelp(w io.Writer, word string, subs []*command) {
	fmt.Fprintf(w, "Usage: %s %s <command> [args] [flags]\n\nCommands:\n", inv.prog, word)
	for _, c := range subs {
		fmt.Fprintf(w, "  %-24s %s\n", c.name(), c.summary)
	}
}

func (inv *invocation) printCommandHelp(w io.Writer, cmd *command) {
	usage := inv.prog + " " + inv.spell(cmd)
	if cmd.args != "" {
		usage += " " + cmd.args
	}
	if len(cmd.flags) > 0 {
		usage += " [flags]"
	}
	fmt.Fprintf(w, "Usage: %s\n\n%s\n", usage, cmd.summary)
	if cmd.legacy != "" {
		fmt.Fprintf(w, "\nAlso: %s %s\n", ProgStdio, cmd.legacy)
	}
	if len(cmd.flags) > 0 {
		fmt.Fprintln(w, "\nFlags:")
		printFlags(w, cmd.flags)
	}
	fmt.Fprintln(w, "\nFlags for every command:")
	printFlags(w, globalFlags)
	if cmd.jsonArgs == nil {
		fmt.Fprintln(w, "\nOutput is text only.")
	}
}

func printFlags(w io.Writer, flags []flagSpec) {
	for _, f := range flags {
		left := f.name
		if f.alias != "" {
			left = f.alias + ", " + f.name
		}
		if f.value != "" {
			left += " " + f.value
		}
		fmt.Fprintf(w, "  %-28s %s\n", left, f.help)
	}
}

func flagNames(cmd *command) []string {
	var out []string
	for _, f := range cmd.flags {
		out = append(out, f.name)
	}
	for _, f := range globalFlags {
		out = append(out, f.name)
	}
	return out
}

func allFirstWords() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range commands {
		for _, w := range []string{c.path[0], c.legacy} {
			if w != "" && !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	sort.Strings(out)
	return out
}

// suggestAs matches a mistyped subcommand against its siblings and names
// the whole command.
func suggestAs(word string, subs []*command) string {
	var last []string
	for _, c := range subs {
		last = append(last, c.path[len(c.path)-1])
	}
	hint := suggest(word, last)
	if hint == "" {
		return ""
	}
	return " (did you mean " + subs[0].path[0] + " " + strings.TrimSuffix(strings.TrimPrefix(hint, " (did you mean "), "?)") + "?)"
}

// suggest names the closest candidate to a mistyped word, or nothing when
// none is close enough to be a typo of it.
func suggest(word string, candidates []string) string {
	best, bestDist := "", -1
	for _, c := range candidates {
		d := editDistance(strings.TrimLeft(word, "-"), strings.TrimLeft(c, "-"))
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}
	limit := len(strings.TrimLeft(word, "-")) / 3
	if limit < 1 {
		limit = 1
	}
	if best == "" || bestDist > limit {
		return ""
	}
	return fmt.Sprintf(" (did you mean %s?)", best)
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
