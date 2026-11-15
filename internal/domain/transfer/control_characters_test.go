package transfer_test

import (
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/transfer"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestADocumentTextHoldingNULIsRefusedInEitherFormat(t *testing.T) {
	cases := map[transfer.Format]string{
		transfer.JSON: `{"schema_version":1,"project":{"key":"shop","name":"Sh\u0000op"}}`,
		transfer.YAML: "schema_version: 1\nproject:\n  key: shop\n  name: \"Sh\\0op\"\n",
	}
	for format, doc := range cases {
		_, err := transfer.Decode(format, []byte(doc))
		e, ok := apperrors.From(err)
		if !ok || e.Code != "invalid_document" || len(e.Violations) != 1 || e.Violations[0].Pointer != "/project/name" || e.Violations[0].Code != "invalid_character" {
			t.Errorf("%s: err = %v, want invalid_document with /project/name invalid_character", format, err)
		}
	}
}
