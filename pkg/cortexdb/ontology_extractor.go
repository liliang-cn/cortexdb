package cortexdb

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// An extractor that is never told the ontology invents a type spelling per
// session: the shared brain grew hundreds of node types, most with one or two
// nodes, while its schema declared a few dozen. ExtractorVocabulary is the
// active schema's object types as an LLM extractor is shown them, so the model
// picks a declared api name (or none) instead of inventing one.
//
// The list is bounded. It is pasted into every extraction prompt, so a schema
// with hundreds of types or essay-length descriptions would crowd out the
// transcript or document being extracted and push small models past their
// context; past these caps the extra types are counted, not listed, and a
// long description is cut.
const (
	// ExtractorTypeLimit is how many object types an extraction prompt lists.
	ExtractorTypeLimit = 60
	// ExtractorDescriptionChars is how many characters of a type's
	// description the prompt keeps.
	ExtractorDescriptionChars = 160
	// ExtractorAliasLimit is how many of a type's aliases the prompt names.
	ExtractorAliasLimit = 5
)

// ExtractorObjectType is one declared object type as an extractor sees it.
type ExtractorObjectType struct {
	APIName     string   `json:"api_name"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
}

// ExtractorTypeVocabulary is the bounded list of object types an extractor is
// told to choose from. The zero value means "no active ontology": every
// prompt built from it is the prompt the extractor always had.
type ExtractorTypeVocabulary struct {
	SchemaID string                `json:"schema_id,omitempty"`
	Types    []ExtractorObjectType `json:"types,omitempty"`
	// Omitted counts declared types past ExtractorTypeLimit.
	Omitted int `json:"omitted,omitempty"`

	// canonical maps every listed type's api name and aliases, folded the way
	// the ontology folds api names, to the api name. It covers omitted types
	// too: a model may still name one, and it is declared.
	canonical map[string]string
}

// ExtractorVocabularyFromSchemas builds the vocabulary from an ontology_list
// active_only answer. Only the first schema counts, as it is the one writes
// are checked against; deprecated types are left out, since an extractor
// should not create new instances of them.
func ExtractorVocabularyFromSchemas(schemas []OntologySchema) ExtractorTypeVocabulary {
	var vocab ExtractorTypeVocabulary
	if len(schemas) == 0 {
		return vocab
	}
	schema := schemas[0]
	vocab.SchemaID = schema.SchemaID
	seen := map[string]bool{}
	for _, objectType := range schema.ObjectTypes {
		apiName := strings.TrimSpace(objectType.APIName)
		key := ontologyAPIKey(apiName)
		if apiName == "" || seen[key] || objectType.Status == OntologyStatusDeprecated {
			continue
		}
		seen[key] = true
		if vocab.canonical == nil {
			vocab.canonical = map[string]string{}
		}
		vocab.canonical[key] = apiName
		var aliases []string
		for _, alias := range objectType.Aliases {
			alias = strings.TrimSpace(alias)
			aliasKey := ontologyAPIKey(alias)
			if alias == "" || aliasKey == key {
				continue
			}
			if _, taken := vocab.canonical[aliasKey]; !taken {
				vocab.canonical[aliasKey] = apiName
			}
			if len(aliases) < ExtractorAliasLimit {
				aliases = append(aliases, alias)
			}
		}
		if len(vocab.Types) >= ExtractorTypeLimit {
			vocab.Omitted++
			continue
		}
		vocab.Types = append(vocab.Types, ExtractorObjectType{
			APIName:     apiName,
			Description: clipExtractorText(objectType.Description, ExtractorDescriptionChars),
			Aliases:     aliases,
		})
	}
	return vocab
}

// ActiveExtractorVocabulary is the vocabulary of the active ontology, empty
// when none is active.
func (db *DB) ActiveExtractorVocabulary(ctx context.Context) (ExtractorTypeVocabulary, error) {
	resp, err := db.ListOntologySchemas(ctx, OntologyListRequest{ActiveOnly: true})
	if err != nil {
		return ExtractorTypeVocabulary{}, err
	}
	return ExtractorVocabularyFromSchemas(resp.Schemas), nil
}

// Empty reports whether there is nothing to tell an extractor.
func (v ExtractorTypeVocabulary) Empty() bool { return len(v.Types) == 0 }

// Canonical returns the declared api name a model's type stands for — the api
// name itself or one of its aliases, in any case — and whether it is declared.
func (v ExtractorTypeVocabulary) Canonical(typeName string) (string, bool) {
	apiName, ok := v.canonical[ontologyAPIKey(strings.TrimSpace(typeName))]
	return apiName, ok
}

// PromptList is the type list as an extraction prompt shows it, one type per
// line; empty when the vocabulary is.
func (v ExtractorTypeVocabulary) PromptList() string {
	if v.Empty() {
		return ""
	}
	var b strings.Builder
	for _, t := range v.Types {
		b.WriteString("- ")
		b.WriteString(t.APIName)
		if t.Description != "" {
			b.WriteString(": ")
			b.WriteString(t.Description)
		}
		if len(t.Aliases) > 0 {
			fmt.Fprintf(&b, " (also called %s)", strings.Join(t.Aliases, ", "))
		}
		b.WriteByte('\n')
	}
	if v.Omitted > 0 {
		fmt.Fprintf(&b, "(%d more declared types are not listed.)\n", v.Omitted)
	}
	return b.String()
}

// clipExtractorText flattens a description to one line and cuts it to max
// runes.
func clipExtractorText(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}
