package main

import (
	"strings"
	"testing"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
)

// TestVersionIsTheOneTheServerWouldReport is the whole value of the flag. An
// operator compares what -version prints against what -health reports from the
// process already running, and decides whether a restart is still pending. If
// those two could drift, the comparison would be worse than no answer: it would
// be a wrong one that looks authoritative.
func TestVersionIsTheOneTheServerWouldReport(t *testing.T) {
	if !strings.Contains(versionLine, cortexdbroot.Version) {
		t.Fatalf("-version prints %q, which does not carry the version the Info RPC reports (%q)",
			versionLine, cortexdbroot.Version)
	}
	if !strings.HasPrefix(versionLine, "cortexdb-grpc v") {
		t.Errorf("-version prints %q; the binary should name itself, because three binaries ship from this module", versionLine)
	}
}

func TestVersionIsAskedTheWayPeopleAskIt(t *testing.T) {
	for _, args := range [][]string{
		{"-version"},
		{"--version"},
	} {
		if !versionRequested(args) {
			t.Errorf("%v did not ask for the version", args)
		}
	}
	for _, args := range [][]string{
		nil,
		{"-health"},
		{"-db", "/tmp/x.db"},
		// Not first is not a request: in `-db --version` the second word is a
		// database path, and starting no server while exiting 0 is the worst
		// possible reading of a typo.
		{"-db", "--version"},
		{"-addr", "127.0.0.1:1", "--version"},
	} {
		if versionRequested(args) {
			t.Errorf("%v was read as a version request", args)
		}
	}
}
