package sinks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

// s3ErrorLimit is the most of an error document read to find its Code.
const s3ErrorLimit = 64 << 10

// ClassifyError reduces why a call got no answer to a failure class, naming
// nothing of the target: neither the text of the error, which can carry the
// name it was made to, nor anything the target said.
func ClassifyError(err error) sinks.Failure {
	var (
		dns     *net.DNSError
		verify  *tls.CertificateVerificationError
		header  tls.RecordHeaderError
		alert   tls.AlertError
		unknown x509.UnknownAuthorityError
		host    x509.HostnameError
		invalid x509.CertificateInvalidError
		timeout interface{ Timeout() bool }
	)
	switch {
	case errors.Is(err, ErrRefusedTarget):
		return sinks.Failure{Class: sinks.ClassRefusedTarget}
	case errors.As(err, &dns):
		return sinks.Failure{Class: sinks.ClassDNS}
	case errors.Is(err, http.ErrSchemeMismatch), errors.As(err, &verify), errors.As(err, &header), errors.As(err, &alert),
		errors.As(err, &unknown), errors.As(err, &host), errors.As(err, &invalid):
		return sinks.Failure{Class: sinks.ClassTLS}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return sinks.Failure{Class: sinks.ClassTimeout}
	default:
		return sinks.Failure{Class: sinks.ClassConnect}
	}
}

// ClassifyResponse reduces a target's answer to a failure class, and whether
// it is a failure: a 2xx is none. It reads of the answer only what the kind's
// protocol puts its error code in: ClickHouse's exception header, and for an
// S3-compatible service the Code of its XML error document, which is read up
// to a limit and not kept. The caller closes the body.
func ClassifyResponse(k sinks.Kind, res *http.Response) (sinks.Failure, bool) {
	switch k {
	case sinks.KindClickHouse:
		return sinks.ClickHouseFailure(res.StatusCode, res.Header.Get("X-ClickHouse-Exception-Code"))
	case sinks.KindObjectStorage:
		code := ""
		if res.StatusCode >= 400 && res.Body != nil {
			code = s3ErrorCode(io.LimitReader(res.Body, s3ErrorLimit))
		}
		return sinks.S3Failure(res.StatusCode, code)
	default:
		return sinks.WebhookFailure(res.StatusCode)
	}
}

// s3ErrorCode reads the Code of the Error element of an S3 error document, and
// nothing else of it; empty when the document is no such thing.
func s3ErrorCode(r io.Reader) string {
	d := xml.NewDecoder(r)
	depth, inCode := 0, false
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 && t.Name.Local != "Error" {
				return ""
			}
			inCode = depth == 2 && t.Name.Local == "Code"
		case xml.EndElement:
			depth--
			inCode = false
		case xml.CharData:
			if inCode {
				return string(t)
			}
		}
	}
}
