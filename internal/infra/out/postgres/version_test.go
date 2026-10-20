package postgres_test

import (
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
)

func TestServerVersionCheck(t *testing.T) {
	for _, tt := range []struct {
		num     int
		refused string
	}{
		{160000, ""},
		{160004, ""},
		{180001, ""},
		{150012, "PostgreSQL 15.12"},
		{90624, "PostgreSQL 9.6.24"},
	} {
		err := postgres.CheckServerVersion(tt.num)
		switch {
		case tt.refused == "" && err != nil:
			t.Errorf("CheckServerVersion(%d) = %v, want nil", tt.num, err)
		case tt.refused != "" && err == nil:
			t.Errorf("CheckServerVersion(%d) accepted an unsupported server", tt.num)
		case tt.refused != "":
			if msg := err.Error(); !strings.Contains(msg, tt.refused) || !strings.Contains(msg, "PostgreSQL 16") {
				t.Errorf("CheckServerVersion(%d) = %q, want it to name %s and PostgreSQL 16", tt.num, msg, tt.refused)
			}
		}
	}
}
