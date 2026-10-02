package cortexdb

import (
	"strings"
	"unicode"
)

// A document's title is an entity of the document.
//
// The default extractor reads chunk text and never sees the title, so a
// passage titled "Lothair II" that says "He was the son of Emperor Lothair I"
// was never linked to the entity its own title names. Multi-hop retrieval
// lives on exactly that link: a question names the entity, the walk starts
// there, and the passage about it is one mention edge away. Measured through
// the public API on 2WikiMultiHopQA and MuSiQue, the graph modes were no better
// than lexical without it — the gains reported for the walk came from a
// benchmark that added each title by hand.
//
// Only a title short enough to be a name qualifies; a sentence-long title is a
// description, not something a question would name. The whole title is the
// entity, never its words one by one: "God's Gift to Women" is one film, not
// three entities called God, Gift and Women.
const (
	titleEntityMaxRunes = 80
	titleEntityMaxWords = 8
)

// documentTitleEntity returns the entity a document's title names, or false
// when the title is empty or does not read as a name.
func documentTitleEntity(title string) (GraphEntity, bool) {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || len([]rune(title)) > titleEntityMaxRunes || len(strings.Fields(title)) > titleEntityMaxWords {
		return GraphEntity{}, false
	}
	// A name has a capital letter or is written in a script that has none.
	named := false
	for _, r := range title {
		if unicode.IsUpper(r) || isCJKRune(r) {
			named = true
			break
		}
	}
	if !named {
		return GraphEntity{}, false
	}
	return GraphEntity{Name: title, Type: "entity"}, true
}
