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

func TestPoolSizePrecedence(t *testing.T) {
	for _, tt := range []struct {
		url      string
		maxConns int
		want     int32
		overrode bool
	}{
		{"postgres://db/flagnetic", 0, postgres.DefaultMaxConns, false},
		{"postgres://db/flagnetic?pool_max_conns=3", 0, 3, false},
		{"postgres://db/flagnetic?pool_max_conns=3", 7, 7, true},
		{"host=db dbname=flagnetic pool_max_conns = 3", 0, 3, false},
		{"host=db dbname=flagnetic pool_max_conns = 3", 7, 7, true},
		{"host=db dbname=flagnetic", 7, 7, false},
	} {
		cfg, overrode, err := postgres.PoolConfig(tt.url, tt.maxConns)
		if err != nil {
			t.Fatalf("PoolConfig(%q): %v", tt.url, err)
		}
		if cfg.MaxConns != tt.want || overrode != tt.overrode {
			t.Errorf("PoolConfig(%q, %d) = %d, overrode %v; want %d, %v", tt.url, tt.maxConns, cfg.MaxConns, overrode, tt.want, tt.overrode)
		}
	}
}
