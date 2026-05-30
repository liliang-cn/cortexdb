package importflow

import (
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// ragChunk is one row prepared for the RAG sink.
type ragChunk struct {
	id        string
	content   string
	metadata  map[string]string
	namespace string
	refine    bool
}

// mapRAG renders a row into a ragChunk; ok is false when content is empty.
func mapRAG(p *RAGPlan, r Record) (ragChunk, bool) {
	if p == nil {
		return ragChunk{}, false
	}
	content := renderTemplate(p.ContentTmpl, r)
	if content == "" {
		return ragChunk{}, false
	}
	id := ""
	if p.IDColumn != "" {
		if v, ok := r.Get(p.IDColumn); ok {
			id = v
		}
	}
	if id == "" {
		id = fmt.Sprintf("%s:%d", r.Table, r.Row)
	}
	md := map[string]string{"_table": r.Table}
	for _, col := range p.Metadata {
		if v, ok := r.Get(col); ok {
			md[col] = v
		}
	}
	return ragChunk{id: id, content: content, metadata: md, namespace: p.Namespace, refine: p.Refine}, true
}

// entityIRI builds a stable IRI for an entity instance.
func entityIRI(typ, id string) string {
	return fmt.Sprintf("urn:cortexdb:%s:%s", typ, id)
}

// mapTriples produces structured RDF triples for a row (rdf:type, labels,
// relations). Entities with an empty rendered ID are skipped.
func mapTriples(p *KGPlan, r Record) []graph.RDFTriple {
	if p == nil {
		return nil
	}
	const (
		rdfType   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
		rdfsLabel = "http://www.w3.org/2000/01/rdf-schema#label"
	)
	refIRI := map[string]string{} // entity ref -> instance IRI
	var triples []graph.RDFTriple
	for _, e := range p.Entities {
		id := renderTemplate(e.IDTmpl, r)
		if id == "" {
			continue
		}
		iri := entityIRI(e.Type, id)
		refIRI[e.Ref] = iri
		triples = append(triples, graph.RDFTriple{
			Subject:   graph.NewIRI(iri),
			Predicate: graph.NewIRI(rdfType),
			Object:    graph.NewIRI("urn:cortexdb:class:" + e.Type),
		})
		if e.LabelTmpl != "" {
			if label := renderTemplate(e.LabelTmpl, r); label != "" {
				triples = append(triples, graph.RDFTriple{
					Subject:   graph.NewIRI(iri),
					Predicate: graph.NewIRI(rdfsLabel),
					Object:    graph.NewLiteral(label),
				})
			}
		}
		for _, prop := range e.Props {
			if v, ok := r.Get(prop); ok {
				triples = append(triples, graph.RDFTriple{
					Subject:   graph.NewIRI(iri),
					Predicate: graph.NewIRI("urn:cortexdb:prop:" + prop),
					Object:    graph.NewLiteral(v),
				})
			}
		}
	}
	for _, rel := range p.Relations {
		s, sok := refIRI[rel.Subject]
		o, ook := refIRI[rel.Object]
		if !sok || !ook {
			continue
		}
		triples = append(triples, graph.RDFTriple{
			Subject:   graph.NewIRI(s),
			Predicate: graph.NewIRI("urn:cortexdb:rel:" + rel.Predicate),
			Object:    graph.NewIRI(o),
		})
	}
	return triples
}
