package graph

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (g *GraphStore) importTurtle(ctx context.Context, reader io.Reader) (int, error) {
	return g.importRDFDocument(ctx, reader, rdfSyntaxTurtle)
}

func (g *GraphStore) importTriG(ctx context.Context, reader io.Reader) (int, error) {
	return g.importRDFDocument(ctx, reader, rdfSyntaxTriG)
}

func (g *GraphStore) importLineStatements(ctx context.Context, reader io.Reader, allowGraph bool) (int, error) {
	syntax := rdfSyntaxNTriples
	if allowGraph {
		syntax = rdfSyntaxNQuads
	}
	return g.importRDFDocument(ctx, reader, syntax)
}

// importRDFDocument parses the whole document before storing any of it, so a
// syntax error halfway down imports nothing rather than the first half.
func (g *GraphStore) importRDFDocument(ctx context.Context, reader io.Reader, syntax rdfSyntax) (int, error) {
	payload, err := io.ReadAll(reader)
	if err != nil {
		return 0, fmt.Errorf("read %s payload: %w", syntax, err)
	}
	parsed, err := parseRDFDocument(string(payload), syntax, g.importBaseIRI())
	if err != nil {
		return 0, err
	}
	triples := make([]*RDFTriple, len(parsed))
	for i := range parsed {
		triples[i] = &parsed[i]
	}
	result, err := g.UpsertTriplesBatch(ctx, triples)
	if err != nil {
		return 0, err
	}
	if result.FailedCount > 0 {
		return result.SuccessCount, result.Errors[0]
	}
	return result.SuccessCount, nil
}

func (g *GraphStore) exportTriG(ctx context.Context, writer io.Writer, triples []RDFTriple) error {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	buffered := bufio.NewWriter(writer)
	defer func() { _ = buffered.Flush() }()

	if err := writeTurtleHeader(buffered, namespaces, triples); err != nil {
		return err
	}
	w := newRDFTermWriter(namespaces, true)

	defaultGraph := make([]RDFTriple, 0)
	graphOrder := make([]string, 0)
	graphBuckets := make(map[string][]RDFTriple)
	graphTerms := make(map[string]RDFTerm)
	for _, triple := range triples {
		if triple.Graph == nil {
			defaultGraph = append(defaultGraph, triple)
			continue
		}
		key := triple.Graph.Kind + "|" + triple.Graph.Value
		if _, ok := graphBuckets[key]; !ok {
			graphOrder = append(graphOrder, key)
			graphTerms[key] = *triple.Graph
		}
		graphBuckets[key] = append(graphBuckets[key], triple)
	}

	if err := writeTriGStatements(w, buffered, defaultGraph, ""); err != nil {
		return err
	}
	if len(defaultGraph) > 0 && len(graphOrder) > 0 {
		if _, err := fmt.Fprintln(buffered); err != nil {
			return err
		}
	}
	for i, key := range graphOrder {
		graphLabel, err := w.term(graphTerms[key])
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(buffered, "%s {\n", graphLabel); err != nil {
			return err
		}
		if err := writeTriGStatements(w, buffered, graphBuckets[key], "\t"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(buffered, "}"); err != nil {
			return err
		}
		if i+1 < len(graphOrder) {
			if _, err := fmt.Fprintln(buffered); err != nil {
				return err
			}
		}
	}
	return buffered.Flush()
}

func writeTriGStatements(w *rdfTermWriter, writer io.Writer, triples []RDFTriple, indent string) error {
	for _, triple := range triples {
		line, err := w.statement(triple, false)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "%s%s\n", indent, line); err != nil {
			return err
		}
	}
	return nil
}

func (g *GraphStore) importBaseIRI() string {
	path := strings.TrimSpace(g.store.Config().Path)
	if path == "" {
		return "file:///"
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	info, err := os.Stat(absPath)
	if err == nil && info.IsDir() {
		return fileURI(absPath) + "/"
	}
	dir := filepath.Dir(absPath)
	return fileURI(dir) + "/"
}

func fileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if strings.HasPrefix(slashed, "/") {
		return "file://" + slashed
	}
	return "file:///" + slashed
}
