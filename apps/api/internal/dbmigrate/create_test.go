package dbmigrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateFilesNumbersAfterHighest(t *testing.T) {
	dir := t.TempDir()
	tenant := filepath.Join(dir, "tenant")
	if err := os.MkdirAll(tenant, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"000001_core.up.sql", "000001_core.down.sql", "000007_x.up.sql", "000007_x.down.sql", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(tenant, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	up, down, err := CreateFiles(dir, Tenant, "  Add  Loyalty/Points! ")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(up) != "000008_add_loyalty_points.up.sql" || filepath.Base(down) != "000008_add_loyalty_points.down.sql" {
		t.Fatalf("got %s and %s", up, down)
	}
	for _, p := range []string{up, down} {
		if b, err := os.ReadFile(p); err != nil || len(b) == 0 {
			t.Fatalf("%s: %v, empty=%v", p, err, len(b) == 0)
		}
	}
}

func TestCreateFilesRejectsEmptyName(t *testing.T) {
	if _, _, err := CreateFiles(t.TempDir(), Control, " !!! "); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseKind(t *testing.T) {
	for in, want := range map[string]Kind{"control": Control, "tenant": Tenant} {
		if got, err := ParseKind(in); err != nil || got != want {
			t.Errorf("ParseKind(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseKind("shared"); err == nil {
		t.Error("expected an error for an unknown kind")
	}
}
