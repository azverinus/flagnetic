package operations

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "write the operation table from the document")

const documentPath = "../../../../../../api/management.yaml"

// TestTheOperationTableIsTheDocument: the table the permission seam reads is
// generated from the document's annotations, and `task generate` writes it.
func TestTheOperationTableIsTheDocument(t *testing.T) {
	doc, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := Read(doc)
	if err != nil {
		t.Fatalf("the document does not declare what every operation requires:\n%v", err)
	}
	want, err := render(ops)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err = os.WriteFile("table.gen.go", want, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile("table.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("table.gen.go is stale; run task generate")
	}
}

func TestEveryOperationOfTheDocumentIsInTheTableAndServed(t *testing.T) {
	doc, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := Read(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != len(Table) {
		t.Errorf("the document has %d operations, the table %d", len(ops), len(Table))
	}
	for pattern, op := range ops {
		if Table[pattern] != op {
			t.Errorf("%s: table holds %+v, the document says %+v", pattern, Table[pattern], op)
		}
	}
}

func TestAnOperationMustDeclareAKnownPermission(t *testing.T) {
	for name, operation := range map[string]string{
		"no annotation":            "operationId: listThings",
		"unknown permission":       "operationId: listThings\n      x-permission: thing.read",
		"a role, not a permission": "operationId: listThings\n      x-permission: owner",
		"no identifier":            "x-permission: member",
	} {
		doc := "paths:\n  /things:\n    parameters: []\n    get:\n      " + operation + "\n"
		_, err := Read([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), "GET /things") {
			t.Errorf("%s: err = %v, want the operation refused", name, err)
		}
	}
}

func TestAnUnreadableDocumentIsRefused(t *testing.T) {
	for name, doc := range map[string]string{
		"not YAML":                   "paths: [",
		"a session flag not boolean": "paths:\n  /me:\n    patch:\n      operationId: updateMe\n      x-permission: member\n      x-session-required: maybe\n",
	} {
		if _, err := Read([]byte(doc)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTheSessionFlagAndTheScopeAreRead(t *testing.T) {
	doc := "paths:\n  /members/{member}:\n    patch:\n      operationId: updateMember\n      x-permission: instance_admin\n      x-session-required: true\n"
	ops, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	op := ops["PATCH /members/{member}"]
	if !op.SessionRequired || op.Read || op.Scope != "instance:admin" {
		t.Errorf("operation = %+v", op)
	}
}

func TestOnlyReadingWhoTheCallerIsIsOpenToProjectTokens(t *testing.T) {
	doc := "paths:\n  /me:\n    get:\n      operationId: getMe\n      x-permission: member\n    patch:\n      operationId: updateMe\n      x-permission: member\n      x-session-required: true\n  /roles:\n    get:\n      operationId: listRoles\n      x-permission: member\n"
	ops, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for pattern, want := range map[string]bool{"GET /me": true, "PATCH /me": false, "GET /roles": false} {
		if got := ops[pattern].OpenToProjectTokens; got != want {
			t.Errorf("%s: open to project tokens = %v, want %v", pattern, got, want)
		}
	}
}

func TestTheOperationsOpeningASessionAndCheckingAPasswordAreRead(t *testing.T) {
	doc := "paths:\n  /auth/sign-in:\n    post:\n      operationId: signIn\n      x-permission: public\n      x-opens-session: true\n      x-checks-password: true\n" +
		"  /me/password:\n    post:\n      operationId: changeMyPassword\n      x-permission: member\n      x-checks-password: true\n" +
		"  /auth/sign-out:\n    post:\n      operationId: signOut\n      x-permission: member\n"
	ops, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for pattern, want := range map[string][2]bool{
		"POST /auth/sign-in": {true, true}, "POST /me/password": {false, true}, "POST /auth/sign-out": {false, false},
	} {
		if got := ops[pattern]; got.OpensSession != want[0] || got.ChecksPassword != want[1] {
			t.Errorf("%s: opens a session %v, checks a password %v; want %v", pattern, got.OpensSession, got.ChecksPassword, want)
		}
	}
}

// A leaked token must neither mint another credential nor change its own
// member's account, so every write under /me and every operation creating a
// token needs a session.
func TestEveryWriteToMeAndEveryTokenCreationNeedsASession(t *testing.T) {
	for pattern, e := range Table {
		method, path, _ := strings.Cut(pattern, " ")
		createsToken := method == "POST" && strings.HasSuffix(path, "/tokens")
		writesMe := method != "GET" && (path == "/me" || strings.HasPrefix(path, "/me/"))
		if (createsToken || writesMe) && !e.SessionRequired {
			t.Errorf("%s (%s) does not require a session", pattern, e.Name)
		}
	}
}

func TestAnOperationsOwnBodyLimitIsRead(t *testing.T) {
	doc := "paths:\n  /projects/{project}/hash-check:\n    post:\n      operationId: checkHashUniformity\n      x-permission: project.read\n      x-max-body-bytes: 67108864\n" +
		"  /projects:\n    post:\n      operationId: createProject\n      x-permission: instance_admin\n"
	ops, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := ops["POST /projects/{project}/hash-check"].MaxBodyBytes; got != 64<<20 {
		t.Errorf("hash-check's body limit = %d, want 64 MiB", got)
	}
	if got := ops["POST /projects"].MaxBodyBytes; got != 0 {
		t.Errorf("a body limit nobody declared = %d, want 0", got)
	}
}
