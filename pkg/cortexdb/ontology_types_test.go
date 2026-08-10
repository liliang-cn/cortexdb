package cortexdb

import (
	"encoding/json"
	"testing"
)

func TestOntologySchemaJSONRoundTrip(t *testing.T) {
	schema := OntologySchema{
		SchemaID: "aviation",
		Name:     "Aviation",
		Version:  1,
		Active:   true,
		ObjectTypes: []OntologyObjectType{
			{
				APIName:           "Airport",
				DisplayName:       "Airport",
				PluralDisplayName: "Airports",
				Status:            OntologyStatusActive,
				Visibility:        OntologyVisibilityNormal,
				PrimaryKey:        "iataCode",
				TitleProperty:     "airportName",
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "airportName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, Searchable: true},
					{APIName: "elevation", DataType: OntologyDataType{Kind: OntologyDataInteger}},
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}},
					{APIName: "embedding", DataType: OntologyDataType{Kind: OntologyDataVector, Dimension: 768}},
				},
			},
		},
		LinkTypes: []OntologyLinkType{
			{
				APIName: "flightDeparture",
				A:       OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
				B:       OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne, ForeignKeyProperty: "originIata"},
			},
		},
	}

	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded OntologySchema
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ObjectTypes[0].PrimaryKey != "iataCode" {
		t.Fatalf("primary key lost: %q", decoded.ObjectTypes[0].PrimaryKey)
	}
	if decoded.ObjectTypes[0].Properties[4].DataType.Dimension != 768 {
		t.Fatalf("vector dimension lost: %d", decoded.ObjectTypes[0].Properties[4].DataType.Dimension)
	}
	if decoded.LinkTypes[0].B.Cardinality != OntologyCardinalityOne {
		t.Fatalf("cardinality lost: %q", decoded.LinkTypes[0].B.Cardinality)
	}
	if decoded.LinkTypes[0].B.ForeignKeyProperty != "originIata" {
		t.Fatalf("foreign key lost: %q", decoded.LinkTypes[0].B.ForeignKeyProperty)
	}
}

func TestOntologyDataTypeNestedKinds(t *testing.T) {
	dt := OntologyDataType{
		Kind:     OntologyDataArray,
		ItemType: &OntologyDataType{Kind: OntologyDataString},
	}
	encoded, err := json.Marshal(dt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded OntologyDataType
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ItemType == nil || decoded.ItemType.Kind != OntologyDataString {
		t.Fatalf("item type lost: %+v", decoded.ItemType)
	}
}
