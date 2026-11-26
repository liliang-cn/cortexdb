package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Shell completion. Each script hands the words typed so far to the hidden
// `cortexdb __complete` and offers what it prints, so the shells complete
// from the same command table and the same tool list as everything else,
// and a new command or tool needs no new script.

const bashCompletion = `# cortexdb completion for bash: source <(cortexdb completion bash)
_cortexdb() {
  local IFS=$'\n'
  COMPREPLY=($(cortexdb __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null))
}
complete -o default -o nospace -F _cortexdb cortexdb
`

const zshCompletion = `#compdef cortexdb
# cortexdb completion for zsh: source <(cortexdb completion zsh), after compinit
_cortexdb() {
  local -a items
  items=("${(@f)$(cortexdb __complete "${(@)words[2,CURRENT]}" 2>/dev/null)}")
  [[ -n "${items[1]}" ]] && compadd -S '' -- "${items[@]}" || _files
}
compdef _cortexdb cortexdb
`

const fishCompletion = `# cortexdb completion for fish: cortexdb completion fish | source
complete -c cortexdb -f -a '(cortexdb __complete (commandline -opc)[2..-1] (commandline -ct) | string trim -r)'
`

func runCompletion(inv *invocation, args []string) int {
	switch positionals(inv.cmd, args)[0] {
	case "bash":
		fmt.Fprint(inv.stdout, bashCompletion)
	case "zsh":
		fmt.Fprint(inv.stdout, zshCompletion)
	case "fish":
		fmt.Fprint(inv.stdout, fishCompletion)
	default:
		return inv.usageError(inv.cmd, "the shells are bash, zsh and fish")
	}
	return exitOK
}

// completeWords answers `__complete w1 ... partial`: the candidates for the
// last word, one per line. Words that end in a space are finished; the
// others are completed further (a group name, KEY= of --arg).
func completeWords(words []string, toolNames func() []string, toolArgs func(tool string) []string) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	partial := words[len(words)-1]
	done := words[:len(words)-1]
	// The common flags may sit anywhere; drop them and their values so the
	// command is found where it is.
	var cmdWords []string
	for i := 0; i < len(done); i++ {
		if f, ok := globalFlag(done[i]); ok {
			if f.value != "" && !strings.Contains(done[i], "=") {
				i++
			}
			continue
		}
		cmdWords = append(cmdWords, done[i])
	}

	if len(done) > 0 {
		if f, ok := globalFlag(done[len(done)-1]); ok && f.name == "--format" {
			return withPrefix([]string{"text ", "json "}, partial)
		}
	}

	cmd, rest := lookupCommand(cmdWords)
	if cmd == nil {
		if len(cmdWords) == 0 {
			if strings.HasPrefix(partial, "-") {
				return withPrefix(spaced(flagList(nil)), partial)
			}
			return withPrefix(firstWordCandidates(), partial)
		}
		if len(cmdWords) == 1 {
			var out []string
			for _, c := range subcommandsOf(cmdWords[0]) {
				out = append(out, c.path[1]+" ")
			}
			return withPrefix(out, partial)
		}
		return nil
	}

	// The value of a flag: --format and --scope have fixed ones; --arg
	// completes the tool's argument names; others are left to the shell's
	// file completion.
	if len(rest) > 0 {
		last := rest[len(rest)-1]
		if f, ok := cmd.flag(last); ok && f.value != "" {
			switch f.name {
			case "--scope":
				return withPrefix([]string{"global ", "user ", "session "}, partial)
			case "--retrieval-mode":
				return withPrefix([]string{"auto ", "lexical ", "graph ", "ppr "}, partial)
			case "--arg":
				if p := positionals(cmd, rest); len(p) > 0 && toolArgs != nil {
					var out []string
					for _, a := range toolArgs(p[0]) {
						out = append(out, a+"=")
					}
					return withPrefix(out, partial)
				}
			}
			return nil
		}
	}

	if strings.HasPrefix(partial, "-") {
		return withPrefix(spaced(flagList(cmd)), partial)
	}
	if len(cmd.path) == 2 && cmd.path[0] == "tool" && cmd.path[1] != "list" && len(positionals(cmd, rest)) == 0 && toolNames != nil {
		return withPrefix(spaced(toolNames()), partial)
	}
	if cmd.name() == "completion" && len(positionals(cmd, rest)) == 0 {
		return withPrefix([]string{"bash ", "zsh ", "fish "}, partial)
	}
	if cmd.name() == "help" {
		return withPrefix(firstWordCandidates(), partial)
	}
	return nil
}

func firstWordCandidates() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range commands {
		w := c.path[0] + " "
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

func flagList(cmd *command) []string {
	var out []string
	if cmd != nil {
		for _, f := range cmd.flags {
			out = append(out, f.name)
		}
	}
	for _, f := range globalFlags {
		out = append(out, f.name)
	}
	return out
}

func spaced(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w + " "
	}
	return out
}

func withPrefix(candidates []string, partial string) []string {
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, partial) {
			out = append(out, c)
		}
	}
	return out
}

// runComplete is `__complete`: hidden, called by the completion scripts.
// Tool names and arguments come from the brain the flags and environment
// name, under a short deadline so a slow brain stalls no keystroke for long.
func runComplete(inv *invocation, words []string) int {
	var session *toolSession
	open := func() *toolSession {
		if session == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if s, err := openToolSession(ctx); err == nil {
				session = s
			}
		}
		return session
	}
	defer func() {
		if session != nil {
			session.close()
		}
	}()
	toolNames := func() []string {
		s := open()
		if s == nil {
			return nil
		}
		tools, err := s.tools(context.Background())
		if err != nil {
			return nil
		}
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name
		}
		return names
	}
	toolArgs := func(name string) []string {
		s := open()
		if s == nil {
			return nil
		}
		t, err := s.tool(context.Background(), name)
		if err != nil {
			return nil
		}
		var keys []string
		for k := range toolSchema(t).Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	for _, c := range completeWords(words, toolNames, toolArgs) {
		fmt.Fprintln(inv.stdout, c)
	}
	return exitOK
}
