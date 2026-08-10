package cortexdb

import "testing"

func TestValidateOntologyAPIName(t *testing.T) {
	valid := []string{"Airport", "flightDeparture", "a", "Object_Type_1"}
	for _, name := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := validateOntologyAPIName("object type", name); err != nil {
				t.Fatalf("expected %q to be valid, got %v", name, err)
			}
		})
	}

	invalid := []string{"", " ", "1Airport", "flight-departure", "flight departure", "flight.departure", "航班"}
	for _, name := range invalid {
		t.Run("invalid/"+name, func(t *testing.T) {
			if err := validateOntologyAPIName("object type", name); err == nil {
				t.Fatalf("expected %q to be rejected", name)
			}
		})
	}
}

func TestOntologyAPIKeyIsCaseInsensitive(t *testing.T) {
	if ontologyAPIKey("Airport") != ontologyAPIKey("airport") {
		t.Fatal("api key lookup must be case-insensitive")
	}
	if ontologyAPIKey("Airport") == ontologyAPIKey("Aircraft") {
		t.Fatal("distinct names must not collide")
	}
}

func TestParseOntologyPropertyValue(t *testing.T) {
	cases := []struct {
		kind    OntologyDataKind
		raw     string
		wantErr bool
	}{
		{OntologyDataString, "LHR", false},
		{OntologyDataInteger, "83", false},
		{OntologyDataInteger, "83.5", true},
		{OntologyDataInteger, "not-a-number", true},
		{OntologyDataDouble, "83.5", false},
		{OntologyDataBoolean, "true", false},
		{OntologyDataBoolean, "yes", true},
		{OntologyDataTimestamp, "2026-08-10T12:00:00Z", false},
		{OntologyDataTimestamp, "10/08/2026", true},
		{OntologyDataDate, "2026-08-10", false},
		{OntologyDataGeoPoint, "51.4700,-0.4543", false},
		{OntologyDataGeoPoint, "51.4700", true},
		{OntologyDataString, "", true},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind)+"/"+tc.raw, func(t *testing.T) {
			err := parseOntologyPropertyValue(OntologyDataType{Kind: tc.kind}, tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("kind=%s raw=%q: expected error", tc.kind, tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("kind=%s raw=%q: unexpected error %v", tc.kind, tc.raw, err)
			}
		})
	}
}
