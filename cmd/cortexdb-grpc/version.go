package main

import (
	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
)

// versionLine is what -version prints: the binary's name and the version it was
// built from, in the same `vX.Y.Z` shape the health probe uses for a running
// server, so the two are comparable by eye during an upgrade.
const versionLine = "cortexdb-grpc v" + cortexdbroot.Version

// versionRequested reports whether the arguments ask for the version.
//
// Read off os.Args rather than through the flag package because the answer has
// to come before flag.Parse: parsing resolves every other flag's default, and
// those defaults read the environment and the home directory. A binary should
// be able to say what it is on a machine where none of that is in place yet —
// which is exactly the machine you are on when you have just copied it there.
//
// Only the first argument counts, because without a parser nothing else can be
// told apart from a value: in `-db --version` the second word is a database
// path. Accepting it there would start no server, exit 0, and print a version
// — which under systemd is a unit that looks like it came up. The version is
// asked for on its own or not at all.
//
// Both spellings are accepted. `-version` is Go's, `--version` is what anyone
// arriving from any other tool will type, and being pedantic about the dash
// count would make this less useful than the checksum it replaces.
func versionRequested(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return args[0] == "-version" || args[0] == "--version"
}
