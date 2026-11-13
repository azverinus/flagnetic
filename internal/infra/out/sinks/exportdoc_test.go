package sinks_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	domain "github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// parquetTypes are the Parquet leaf type each type exported-events.md
// documents is written as, in parquet-go's schema notation.
var parquetTypes = map[string]string{
	"string":    "binary %s (STRING)",
	"bool":      "boolean %s",
	"int":       "int64 %s (INT(64,true))",
	"int64":     "int64 %s (INT(64,true))",
	"timestamp": "int64 %s (TIMESTAMP(isAdjustedToUTC=true,unit=MILLIS))",
}

func TestParquetFilesHoldTheDocumentedColumnsAndTypes(t *testing.T) {
	doc := testkit.Doc(t, "exported-events.md")
	store := testkit.StartObjectStorage(t)
	for _, kind := range domain.EventKinds {
		t.Run(string(kind), func(t *testing.T) {
			bucket := store.NewBucket(t)
			c := objectStorageSink(store, store.Endpoint, bucket, func(c *domain.ObjectStorage) { c.Format, c.Compression = "parquet", "zstd" })
			rows := domain.SampleRows(kind, "shop", received)
			if f := objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "dlv_abc", Kind: kind, Rows: rows, Now: received}); f != nil {
				t.Fatalf("Deliver = %v", f)
			}
			keys := store.Keys(t, bucket)
			if len(keys) != 1 {
				t.Fatalf("keys = %v", keys)
			}
			content, _ := store.Object(t, bucket, keys[0])
			file, err := parquet.OpenFile(bytes.NewReader(content), int64(len(content)))
			if err != nil {
				t.Fatal(err)
			}
			schema := file.Schema().String()
			documented := testkit.DocColumns(t, doc, string(kind))
			fields := file.Schema().Fields()
			if len(fields) != len(documented) {
				t.Errorf("the file has %d columns, the page documents %d", len(fields), len(documented))
			}
			for i, col := range documented {
				if i < len(fields) && fields[i].Name() != col.Name {
					t.Errorf("column %d is %s, the page documents %s", i+1, fields[i].Name(), col.Name)
				}
				if want := "required " + fmt.Sprintf(parquetTypes[col.Type], col.Name) + ";"; !strings.Contains(schema, want) {
					t.Errorf("the Parquet schema has no %q:\n%s", want, schema)
				}
			}
		})
	}
}
