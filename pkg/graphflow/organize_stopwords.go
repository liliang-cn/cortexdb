package graphflow

// entityStopwords are common (capitalized) English words that the heuristic
// extractor wrongly treats as entities — typically sentence-initial words,
// generic nouns, verbs, and HTTP/log tokens. Dropping them removes the bulk of
// the noise while leaving proper nouns (Aliyun, Snowflake, Portkey, …) intact.
// Lowercased keys.
var entityStopwords = func() map[string]struct{} {
	words := []string{
		// articles, pronouns, conjunctions, prepositions
		"the", "a", "an", "and", "or", "but", "if", "then", "else", "for", "to", "of",
		"in", "on", "at", "by", "with", "from", "into", "onto", "as", "is", "are", "was",
		"were", "be", "been", "this", "that", "these", "those", "it", "its", "they", "them",
		"we", "you", "he", "she", "his", "her", "our", "your", "their", "i", "me", "my",
		"so", "no", "not", "yes", "all", "any", "each", "both", "few", "more", "most",
		"other", "some", "such", "only", "own", "same", "than", "too", "very", "via", "per",
		// common verbs (often sentence-initial)
		"get", "got", "set", "put", "run", "ran", "use", "used", "add", "added", "make",
		"made", "build", "built", "fix", "fixed", "see", "saw", "show", "shown", "open",
		"close", "start", "stop", "deploy", "deployed", "ship", "shipped", "keep", "kept",
		"move", "moved", "check", "create", "created", "update", "updated", "delete", "send",
		"read", "write", "wrote", "call", "called", "find", "found", "need", "want", "do",
		"done", "go", "going", "let", "try", "tried", "ends", "end", "live", "wired", "drafter",
		// generic nouns / adjectives commonly capitalized in notes & headers
		"home", "quick", "core", "web", "network", "admin", "sample", "verified", "pure",
		"deep", "double", "zero", "one", "two", "three", "first", "last", "next", "prev",
		"new", "old", "true", "false", "none", "null", "main", "test", "tests", "note",
		"notes", "todo", "done", "status", "step", "steps", "part", "case", "type", "name",
		"value", "data", "info", "list", "item", "items", "key", "keys", "id", "url", "uri",
		"file", "files", "path", "dir", "code", "line", "page", "site", "app", "apps", "user",
		"users", "team", "work", "time", "date", "day", "week", "month", "year", "now", "today",
		"english", "faceless", "other", "others", "default", "current", "final", "draft",
		// HTTP / log / shell tokens
		"get", "post", "put", "patch", "head", "options", "ok", "err", "error", "warn",
		"info", "debug", "trace", "fatal", "panic", "exit", "true", "false",
		// more generic/sectioning words & all-caps states seen in real notes
		"there", "here", "stale", "wrong", "right", "hosting", "memory", "footer",
		"header", "ecosystem", "global", "source", "usage", "deleted", "section",
		"content", "summary", "example", "overview", "intro", "introduction",
		"background", "details", "detail", "result", "results", "output", "input",
		"config", "setup", "install", "before", "after", "while", "when", "where",
		"because", "however", "also", "still", "just", "even", "again", "once",
		"what", "why", "how", "who", "which", "must", "role", "things", "thing",
		"should", "could", "would", "will", "shall", "may", "might", "must",
	}
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}()
