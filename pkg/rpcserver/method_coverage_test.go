package rpcserver

import (
	"sort"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
)

// servedMethods is every full method name the server actually registers.
// Register only stores handlers, so a nil *cortexdb.DB is enough to enumerate
// them without opening a database.
func servedMethods(t *testing.T) []string {
	t.Helper()
	s := grpc.NewServer()
	t.Cleanup(s.Stop)
	Register(s, nil, Options{})

	var out []string
	info := s.GetServiceInfo()
	if len(info) == 0 {
		t.Fatal("no services registered; this test would pass vacuously")
	}
	for service, svc := range info {
		for _, m := range svc.Methods {
			out = append(out, "/"+service+"/"+m.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryServedRPCIsClassifiedAsAReadOrAWrite is the test that keeps the
// method table honest. Classifying methods by name — anything starting with
// Delete is a write, say — would silently misclassify the next RPC somebody
// adds, and a write passing as a read is precisely the failure scoped keys
// exist to prevent. So the table is explicit and this walks what the server
// serves to prove nothing escaped it.
func TestEveryServedRPCIsClassifiedAsAReadOrAWrite(t *testing.T) {
	for _, method := range servedMethods(t) {
		m, ok := authz.LookupMethod(method)
		if !ok {
			t.Errorf("%s is served but not classified; add it to the table in pkg/authz/methods.go "+
				"as a read or a write — an unclassified RPC is denied to every key", method)
			continue
		}
		if m.Access != authz.Read && m.Access != authz.Write {
			t.Errorf("%s is in the table with access %s", method, m.Access)
		}
	}
}

// TestEveryClassifiedMethodIsStillServed catches the other drift: an entry left
// behind after an RPC was renamed or removed, which would otherwise sit in the
// table looking like coverage it no longer provides.
func TestEveryClassifiedMethodIsStillServed(t *testing.T) {
	served := make(map[string]struct{})
	for _, m := range servedMethods(t) {
		served[m] = struct{}{}
	}
	for _, method := range authz.ClassifiedMethods() {
		if _, ok := served[method]; !ok {
			t.Errorf("%s is classified but no longer served; drop it from the table", method)
		}
	}
}

// TestEveryRPCDeclaredInTheProtoPackageIsClassified is the earlier warning of
// the two: it reads the generated descriptors rather than the server, so an RPC
// added to a .proto and generated but not yet wired into Register still has to
// be classified before the build goes green.
func TestEveryRPCDeclaredInTheProtoPackageIsClassified(t *testing.T) {
	seen := 0
	protoregistry.GlobalFiles.RangeFilesByPackage("cortexdb.v1", func(fd protoreflect.FileDescriptor) bool {
		services := fd.Services()
		for i := range services.Len() {
			svc := services.Get(i)
			mtds := svc.Methods()
			for j := range mtds.Len() {
				full := "/" + string(svc.FullName()) + "/" + string(mtds.Get(j).Name())
				seen++
				if _, ok := authz.LookupMethod(full); !ok {
					t.Errorf("%s is declared in %s but not classified in pkg/authz/methods.go",
						full, fd.Path())
				}
			}
		}
		return true
	})
	if seen == 0 {
		t.Fatal("no cortexdb.v1 services found in the proto registry; this test would pass vacuously")
	}
}

func TestTheServedMethodListLooksLikeTheWholeAPI(t *testing.T) {
	// A guard against the enumeration silently shrinking — if Register loses a
	// service, the two tests above still pass with nothing left to check.
	want := []string{
		"cortexdb.v1.AdminService",
		"cortexdb.v1.ContractService",
		"cortexdb.v1.GraphRagService",
		"cortexdb.v1.KnowledgeGraphService",
		"cortexdb.v1.KnowledgeService",
		"cortexdb.v1.MemoryService",
		"cortexdb.v1.ToolsService",
	}
	served := servedMethods(t)
	for _, service := range want {
		found := false
		for _, m := range served {
			if strings.HasPrefix(m, "/"+service+"/") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is not registered on the server", service)
		}
	}
}
