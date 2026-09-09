package main

import "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

// storageSchema is the ontology: what may exist in this estate, and what may
// be said about it.
//
// It is written for a replicated block-storage estate — DRBD resources placed
// on LINSTOR nodes out of storage pools — because that domain has an invariant
// worth putting in a schema rather than in a comment: a replicated resource
// may be Primary on AT MOST ONE node at a time. Two primaries is not an
// unusual state, it is the state where two machines write to one disk and the
// filesystem is destroyed.
//
// The whole point of this file is that the invariant is one word. Look at
// resourcePrimary versus resourceReplica below: the two links are the same
// shape and differ only in the cardinality of one side. That difference is
// what the graph will refuse to violate in section 3, and nothing anywhere
// else in this example has to remember it.
func storageSchema() cortexdb.OntologySchema {
	stringType := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	integerType := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataInteger}
	booleanType := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataBoolean}

	return cortexdb.OntologySchema{
		SchemaID:    "sds",
		Name:        "Replicated storage estate",
		Description: "Nodes, storage pools, replicated resources and their volumes.",

		// An interface is how a question is asked of an abstraction rather
		// than of a table. A pool and a volume are different object types with
		// different keys and different lives, and "everything with a size" is
		// a reasonable thing to want — capacity planning does not care which
		// of the two it is looking at.
		InterfaceTypes: []cortexdb.OntologyInterfaceType{
			{
				APIName:     "Sized",
				DisplayName: "Sized",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "displayName", DataType: stringType, Searchable: true},
					{APIName: "sizeGiB", DataType: integerType},
				},
			},
		},

		ObjectTypes: []cortexdb.OntologyObjectType{
			// This one should not have to be here, and it is worth knowing why
			// it is.
			//
			// A strict ontology validates every write to the graph, including
			// the writes CortexDB makes on its own behalf. Saving prose with an
			// embedder runs a built-in extractor over each chunk, and the nodes
			// it produces are typed "entity" — a bookkeeping type, not a domain
			// one. A schema that does not declare it therefore refuses the
			// ingestion, so an active strict ontology and embedder-backed
			// knowledge cannot be used together until somebody adds this.
			//
			// Declaring it is the workaround, not the design. The distinction
			// the product already knows about — domain types versus bookkeeping
			// types, which ontology_draft reasons in — has not reached the
			// validator yet.
			{
				APIName:           "entity",
				PluralDisplayName: "Extracted mentions",
				PrimaryKey:        "name",
				TitleProperty:     "name",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "name", DataType: stringType, Required: true, Searchable: true},
				},
			},
			{
				APIName:           "Node",
				PluralDisplayName: "Nodes",
				PrimaryKey:        "nodeName",
				TitleProperty:     "nodeName",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "nodeName", DataType: stringType, Required: true},
					{APIName: "site", DataType: stringType, Searchable: true},
					{APIName: "nodeRole", DataType: stringType},
				},
			},
			{
				APIName:           "StoragePool",
				PluralDisplayName: "Storage pools",
				PrimaryKey:        "poolKey",
				TitleProperty:     "displayName",
				Implements:        []string{"Sized"},
				Properties: []cortexdb.OntologyProperty{
					{APIName: "poolKey", DataType: stringType, Required: true},
					{APIName: "displayName", DataType: stringType, Required: true, Searchable: true},
					// The foreign key for poolLocation. It is an ordinary
					// property that the link type happens to name.
					{APIName: "onNode", DataType: stringType, Required: true},
					{APIName: "backing", DataType: stringType},
					{APIName: "sizeGiB", DataType: integerType},
					{APIName: "freeGiB", DataType: integerType},
				},
			},
			{
				APIName:           "Resource",
				PluralDisplayName: "Resources",
				PrimaryKey:        "resourceName",
				TitleProperty:     "resourceName",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "resourceName", DataType: stringType, Required: true},
					// Empty is a legitimate value and the ordinary one: a
					// resource that nobody has promoted has no primary. The
					// schema says at most one, not exactly one.
					{APIName: "primaryOn", DataType: stringType},
					{APIName: "quorum", DataType: booleanType},
					// Vectorized routes the value into the vector index, which is
					// what lets an object set filter by MEANING — see the
					// nearest_neighbors predicate in section 7.
					{APIName: "purpose", DataType: stringType, Searchable: true, Vectorized: true},
				},
			},
			{
				APIName:           "Volume",
				PluralDisplayName: "Volumes",
				PrimaryKey:        "volumeKey",
				TitleProperty:     "displayName",
				Implements:        []string{"Sized"},
				Properties: []cortexdb.OntologyProperty{
					{APIName: "volumeKey", DataType: stringType, Required: true},
					{APIName: "displayName", DataType: stringType, Required: true, Searchable: true},
					{APIName: "ofResource", DataType: stringType, Required: true},
					{APIName: "sizeGiB", DataType: integerType},
					{APIName: "minor", DataType: integerType},
				},
			},
		},

		LinkTypes: []cortexdb.OntologyLinkType{
			{
				APIName: "poolLocation",
				A: cortexdb.OntologyLinkSide{
					APIName: "pools", DisplayName: "Pools",
					ObjectTypeAPIName: "Node", Cardinality: cortexdb.OntologyCardinalityMany,
				},
				B: cortexdb.OntologyLinkSide{
					APIName: "node", DisplayName: "Node",
					ObjectTypeAPIName: "StoragePool", Cardinality: cortexdb.OntologyCardinalityOne,
					ForeignKeyProperty: "onNode",
				},
			},

			// THE INVARIANT. A node is primary for many resources; a resource
			// is primary on ONE node. The One is enforced on write, so a
			// second promotion is refused by the database rather than caught
			// by a reviewer — see section 3.
			{
				APIName: "resourcePrimary",
				A: cortexdb.OntologyLinkSide{
					APIName: "primaryFor", DisplayName: "Primary for",
					ObjectTypeAPIName: "Node", Cardinality: cortexdb.OntologyCardinalityMany,
				},
				B: cortexdb.OntologyLinkSide{
					APIName: "primary", DisplayName: "Primary on",
					ObjectTypeAPIName: "Resource", Cardinality: cortexdb.OntologyCardinalityOne,
					ForeignKeyProperty: "primaryOn",
				},
			},

			// The same shape with one word changed. Replication is many to
			// many and always was; writing it beside the link above is how a
			// reader sees that the constraint is a decision and not an
			// accident of modelling.
			{
				APIName: "resourceReplica",
				A: cortexdb.OntologyLinkSide{
					APIName: "replicates", DisplayName: "Replicates",
					ObjectTypeAPIName: "Node", Cardinality: cortexdb.OntologyCardinalityMany,
				},
				B: cortexdb.OntologyLinkSide{
					APIName: "replicaNodes", DisplayName: "Replica nodes",
					ObjectTypeAPIName: "Resource", Cardinality: cortexdb.OntologyCardinalityMany,
				},
			},
			{
				APIName: "volumeOf",
				A: cortexdb.OntologyLinkSide{
					APIName: "volumes", DisplayName: "Volumes",
					ObjectTypeAPIName: "Resource", Cardinality: cortexdb.OntologyCardinalityMany,
				},
				B: cortexdb.OntologyLinkSide{
					APIName: "resource", DisplayName: "Resource",
					ObjectTypeAPIName: "Volume", Cardinality: cortexdb.OntologyCardinalityOne,
					ForeignKeyProperty: "ofResource",
				},
			},
		},

		ActionTypes: []cortexdb.OntologyActionType{
			{
				APIName:     "registerNode",
				DisplayName: "Register node",
				Description: "Add a LINSTOR node to the estate.",
				Parameters: []cortexdb.OntologyActionParameter{
					{APIName: "nodeName", DataType: stringType, Required: true,
						Description: "Lowercase DNS label."},
					{APIName: "site", DataType: stringType, Required: true},
					{APIName: "nodeRole", DataType: stringType},
				},
				Rules: []cortexdb.OntologyActionRule{{
					Kind:       cortexdb.ActionRuleCreateObject,
					ObjectType: "Node",
					PropertyValues: map[string]cortexdb.OntologyValueSource{
						"nodeName": {Kind: cortexdb.ValueSourceParameter, Parameter: "nodeName"},
						"site":     {Kind: cortexdb.ValueSourceParameter, Parameter: "site"},
						"nodeRole": {Kind: cortexdb.ValueSourceParameter, Parameter: "nodeRole"},
					},
				}},
				// Criteria read the parameters only, never the graph, so a
				// validate_only call costs no queries and writes nothing.
				SubmissionCriteria: []cortexdb.OntologySubmissionCriterion{{
					Parameter:      "nodeName",
					Regex:          "^[a-z][a-z0-9-]{1,30}$",
					FailureMessage: "A node name must be a lowercase DNS label.",
				}},
			},
			{
				APIName:     "promoteResource",
				DisplayName: "Promote resource",
				Description: "Make a node the Primary for a resource.",
				Parameters: []cortexdb.OntologyActionParameter{
					{APIName: "resource", DataType: stringType, Required: true, ObjectType: "Resource"},
					{APIName: "toNode", DataType: stringType, Required: true},
				},
				Rules: []cortexdb.OntologyActionRule{{
					Kind:       cortexdb.ActionRuleModifyObject,
					ObjectType: "Resource",
					Target:     "resource",
					PropertyValues: map[string]cortexdb.OntologyValueSource{
						"primaryOn": {Kind: cortexdb.ValueSourceParameter, Parameter: "toNode"},
					},
				}},
				SubmissionCriteria: []cortexdb.OntologySubmissionCriterion{{
					Parameter:      "toNode",
					Regex:          "^[a-z][a-z0-9-]{1,30}$",
					FailureMessage: "A node name must be a lowercase DNS label.",
				}},
			},
		},
	}
}
