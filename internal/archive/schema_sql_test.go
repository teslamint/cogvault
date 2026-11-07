package archive

import (
	"fmt"
	"testing"
)

// The version statement is a literal so no value is formatted into SQL. It
// must say the version the code migrates to, and a registry must end up there.
func TestSetSchemaVersionSQLMatchesSchemaVersion(t *testing.T) {
	if want := fmt.Sprintf("PRAGMA user_version = %d", schemaVersion); setSchemaVersionSQL != want {
		t.Fatalf("setSchemaVersionSQL = %q, want %q", setSchemaVersionSQL, want)
	}
	a, _ := openTest(t)
	var v int
	if err := a.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
}
