package management

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// SeamFor returns next behind the permission seam judging op, so a test can
// judge an operation the document does not declare yet, such as a public
// one.
func SeamFor(edge *flaghttp.Server, h Handlers, op access.Operation, next http.Handler) http.Handler {
	a := newAPI(edge, h, newRefusals(prometheus.NewRegistry()), Options{})
	return a.seam(op, next)
}
