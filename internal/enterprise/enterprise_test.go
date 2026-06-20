package enterprise

import "testing"

func TestDefaultRoles(t *testing.T) {
	for _, name := range []string{"admin", "dev", "viewer", "auditor"} {
		if _, ok := DefaultRoles[name]; !ok {
			t.Errorf("%s role missing", name)
		}
	}
}

func TestAuthorizer(t *testing.T) {
	a := NewAuthorizer(map[string]string{
		"alice": "admin",
		"bob":   "dev",
		"eve":   "viewer",
	})
	if !a.CanRead("alice") || !a.CanWrite("alice") {
		t.Error("admin: read+write should be true")
	}
	if !a.CanRead("bob") || !a.CanWrite("bob") {
		t.Error("dev: read+write should be true")
	}
	if !a.CanRead("eve") || a.CanWrite("eve") {
		t.Error("viewer: read=true, write=false")
	}
	if a.CanRead("unknown") || a.CanWrite("unknown") {
		t.Error("unknown user: all false")
	}
}

func TestAuditExporter(t *testing.T) {
	e := &AuditExporter{}
	e.Add(AuditEntry{Action: "read", ToolName: "bash", ProofHash: "abc"})
	data, _ := e.ExportJSON()
	if len(data) == 0 {
		t.Error("export JSON should not be empty")
	}
	if e.ExportSplunk() == "" {
		t.Error("export Splunk should not be empty")
	}
}

func TestSessionStore(t *testing.T) {
	s := NewSessionStore()
	s.Save("s1", []byte("data"))
	d, err := s.Load("s1")
	if err != nil || string(d) != "data" {
		t.Fatalf("load: %v, got %q", err, d)
	}
	if _, err := s.Load("nope"); err == nil {
		t.Error("load nonexistent should error")
	}
	if len(s.List()) != 1 {
		t.Error("list should have 1 session")
	}
	s.Delete("s1")
	if len(s.List()) != 0 {
		t.Error("list should be empty after delete")
	}
}

func TestDecodeBase64URL(t *testing.T) {
	data, err := decodeBase64URL("aGVsbG8") // "hello"
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q", data)
	}
}

func TestExtractBearer(t *testing.T) {
	if extractBearer("Bearer abc") != "abc" {
		t.Error("bearer extraction failed")
	}
	if extractBearer("Not x") != "" {
		t.Error("non-bearer should return empty")
	}
}
