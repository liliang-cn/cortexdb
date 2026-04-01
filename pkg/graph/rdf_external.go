package graph

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	rdfnq "github.com/0x51-dev/rdf/nquads"
	rdfnt "github.com/0x51-dev/rdf/ntriples"
	rdftrig "github.com/0x51-dev/rdf/trig"
	rdfttl "github.com/0x51-dev/rdf/turtle"
)

func (g *GraphStore) importTurtle(ctx context.Context, reader io.Reader) (int, error) {
	payload, err := io.ReadAll(reader)
	if err != nil {
		return 0, fmt.Errorf("read turtle payload: %w", err)
	}
	doc, err := rdfttl.ParseDocument(string(payload))
	if err != nil {
		return 0, fmt.Errorf("parse turtle document: %w", err)
	}
	triples, err := rdfttl.EvaluateDocument(doc, g.importBaseIRI())
	if err != nil {
		return 0, fmt.Errorf("evaluate turtle document: %w", err)
	}
	return g.upsertExternalTriples(ctx, triples)
}

func (g *GraphStore) importTriG(ctx context.Context, reader io.Reader) (int, error) {
	payload, err := io.ReadAll(reader)
	if err != nil {
		return 0, fmt.Errorf("read trig payload: %w", err)
	}
	doc, err := rdftrig.ParseDocument(string(payload))
	if err != nil {
		return 0, fmt.Errorf("parse trig document: %w", err)
	}
	quads, err := rdftrig.EvaluateDocument(doc)
	if err != nil {
		return 0, fmt.Errorf("evaluate trig document: %w", err)
	}
	return g.upsertExternalQuads(ctx, quads)
}

func (g *GraphStore) upsertExternalTriples(ctx context.Context, doc rdfnt.Document) (int, error) {
	triples := make([]*RDFTriple, 0, len(doc))
	for _, triple := range doc {
		converted, err := externalTripleToRDFTriple(triple)
		if err != nil {
			return 0, err
		}
		triples = append(triples, converted)
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

func (g *GraphStore) upsertExternalQuads(ctx context.Context, doc rdfnq.Document) (int, error) {
	triples := make([]*RDFTriple, 0, len(doc))
	for _, quad := range doc {
		converted, err := externalQuadToRDFTriple(quad)
		if err != nil {
			return 0, err
		}
		triples = append(triples, converted)
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

func externalTripleToRDFTriple(triple rdfnt.Triple) (*RDFTriple, error) {
	subject, err := externalSubjectToTerm(triple.Subject)
	if err != nil {
		return nil, err
	}
	predicate, err := externalPredicateToTerm(triple.Predicate)
	if err != nil {
		return nil, err
	}
	object, err := externalObjectToTerm(triple.Object)
	if err != nil {
		return nil, err
	}
	return &RDFTriple{
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
	}, nil
}

func externalQuadToRDFTriple(quad rdfnq.Quad) (*RDFTriple, error) {
	triple, err := externalTripleToRDFTriple(quad.Triple)
	if err != nil {
		return nil, err
	}
	if quad.GraphLabel != nil {
		graphTerm, err := externalSubjectToTerm(quad.GraphLabel)
		if err != nil {
			return nil, err
		}
		triple.Graph = &graphTerm
	}
	return triple, nil
}

func externalSubjectToTerm(subject rdfnt.Subject) (RDFTerm, error) {
	switch value := subject.(type) {
	case rdfnt.IRIReference:
		return NewIRI(string(value)), nil
	case *rdfnt.IRIReference:
		if value == nil {
			return RDFTerm{}, fmt.Errorf("nil iri subject")
		}
		return NewIRI(string(*value)), nil
	case rdfnt.BlankNode:
		return NewBlankNode(string(value)), nil
	case *rdfnt.BlankNode:
		if value == nil {
			return RDFTerm{}, fmt.Errorf("nil blank subject")
		}
		return NewBlankNode(string(*value)), nil
	default:
		return RDFTerm{}, fmt.Errorf("unsupported rdf subject type %T", subject)
	}
}

func externalPredicateToTerm(predicate rdfnt.IRIReference) (RDFTerm, error) {
	return NewIRI(string(predicate)), nil
}

func externalObjectToTerm(object rdfnt.Object) (RDFTerm, error) {
	switch value := object.(type) {
	case rdfnt.IRIReference:
		return NewIRI(string(value)), nil
	case *rdfnt.IRIReference:
		if value == nil {
			return RDFTerm{}, fmt.Errorf("nil iri object")
		}
		return NewIRI(string(*value)), nil
	case rdfnt.BlankNode:
		return NewBlankNode(string(value)), nil
	case *rdfnt.BlankNode:
		if value == nil {
			return RDFTerm{}, fmt.Errorf("nil blank object")
		}
		return NewBlankNode(string(*value)), nil
	case rdfnt.Literal:
		return externalLiteralToTerm(value), nil
	case *rdfnt.Literal:
		if value == nil {
			return RDFTerm{}, fmt.Errorf("nil literal object")
		}
		return externalLiteralToTerm(*value), nil
	default:
		return RDFTerm{}, fmt.Errorf("unsupported rdf object type %T", object)
	}
}

func externalLiteralToTerm(literal rdfnt.Literal) RDFTerm {
	if literal.Reference != nil {
		return NewTypedLiteral(literal.Value, string(*literal.Reference))
	}
	if literal.Language != "" {
		return NewLangLiteral(literal.Value, literal.Language)
	}
	return NewLiteral(literal.Value)
}

func (g *GraphStore) exportTriG(ctx context.Context, writer io.Writer, triples []RDFTriple) error {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	buffered := bufio.NewWriter(writer)
	defer func() { _ = buffered.Flush() }()

	for _, ns := range namespaces {
		if _, err := fmt.Fprintf(buffered, "@prefix %s: <%s> .\n", ns.Prefix, ns.URI); err != nil {
			return err
		}
	}
	if len(namespaces) > 0 && len(triples) > 0 {
		if _, err := fmt.Fprintln(buffered); err != nil {
			return err
		}
	}

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

	if err := g.writeTriGStatements(ctx, buffered, defaultGraph, ""); err != nil {
		return err
	}
	if len(defaultGraph) > 0 && len(graphOrder) > 0 {
		if _, err := fmt.Fprintln(buffered); err != nil {
			return err
		}
	}
	for i, key := range graphOrder {
		graphLabel, err := g.compactTerm(ctx, graphTerms[key])
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(buffered, "%s {\n", graphLabel); err != nil {
			return err
		}
		if err := g.writeTriGStatements(ctx, buffered, graphBuckets[key], "\t"); err != nil {
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

func (g *GraphStore) writeTriGStatements(ctx context.Context, writer io.Writer, triples []RDFTriple, indent string) error {
	for _, triple := range triples {
		subject, err := g.compactTerm(ctx, triple.Subject)
		if err != nil {
			return err
		}
		predicate, err := g.compactTerm(ctx, triple.Predicate)
		if err != nil {
			return err
		}
		object, err := g.compactTerm(ctx, triple.Object)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "%s%s %s %s .\n", indent, subject, predicate, object); err != nil {
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
