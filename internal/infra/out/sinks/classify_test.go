package sinks_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	domain "github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/out/sinks"
)

// answered makes a call to a target that answers with handler and returns
// how the kind's classifier reduces it, with the text the failure would be
// shown as.
func answered(t *testing.T, k domain.Kind, handler http.HandlerFunc) (domain.Failure, bool, string) {
	t.Helper()
	srv, _ := target(t, handler)
	res, err := call(t, sinks.NewClient(k, sinks.Options{Policy: governance.TargetPolicy{AllowPrivateNetworks: true}}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	failure, failed := sinks.ClassifyResponse(k, res)
	return failure, failed, failure.Error()
}

const secretBody = "internal-admin-panel: password=hunter2"

func TestAServerErrorIsAStatusAndNoText(t *testing.T) {
	for _, k := range domain.Kinds {
		failure, failed, text := answered(t, k, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(secretBody))
		})
		if !failed || failure != (domain.Failure{Class: domain.ClassStatus, Status: 500}) {
			t.Errorf("%s: %+v, %t, want status 500", k, failure, failed)
		}
		if strings.Contains(text, "hunter2") || strings.Contains(text, "admin") {
			t.Errorf("%s: the failure carries the target's text: %q", k, text)
		}
	}
}

func TestASuccessIsNoFailure(t *testing.T) {
	for _, k := range domain.Kinds {
		if failure, failed, _ := answered(t, k, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("fine")) }); failed {
			t.Errorf("%s: %+v", k, failure)
		}
	}
}

func TestAWebhookReceiverRefusingTheCallIsARejection(t *testing.T) {
	failure, failed, _ := answered(t, domain.KindWebhook, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })
	if !failed || failure != (domain.Failure{Class: domain.ClassRejected, Status: 400}) {
		t.Errorf("%+v, %t", failure, failed)
	}
}

func TestAClickHouseExceptionIsReadFromItsHeaderAndNeverItsBody(t *testing.T) {
	failure, failed, text := answered(t, domain.KindClickHouse, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Exception-Code", "16")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Code: 16. DB::Exception: No such column " + secretBody))
	})
	want := domain.Failure{Class: domain.ClassRejected, Status: 500, Code: "16 NO_SUCH_COLUMN_IN_TABLE"}
	if !failed || failure != want {
		t.Errorf("%+v, %t, want %+v", failure, failed, want)
	}
	if strings.Contains(text, "hunter2") || strings.Contains(text, "DB::") {
		t.Errorf("the failure carries the target's text: %q", text)
	}
}

func TestAClickHouseAnswerWithAForgedHeaderCarriesNoText(t *testing.T) {
	failure, _, text := answered(t, domain.KindClickHouse, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Exception-Code", secretBody)
		w.WriteHeader(http.StatusBadGateway)
	})
	if failure != (domain.Failure{Class: domain.ClassStatus, Status: 502}) || strings.Contains(text, "hunter2") {
		t.Errorf("%+v, %q", failure, text)
	}
}

func TestAnS3ErrorIsReadFromTheCodeOfItsErrorDocumentAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   domain.Failure
	}{
		"a missing bucket": {404, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchBucket</Code><Message>The bucket ` + secretBody + `</Message><BucketName>b</BucketName></Error>`,
			domain.Failure{Class: domain.ClassRejected, Status: 404, Code: "NoSuchBucket"}},
		"a code after a message": {403, `<Error><Message>` + secretBody + `</Message><Code>AccessDenied</Code></Error>`,
			domain.Failure{Class: domain.ClassRejected, Status: 403, Code: "AccessDenied"}},
		"a busy service":              {503, `<Error><Code>SlowDown</Code></Error>`, domain.Failure{Class: domain.ClassStatus, Status: 503, Code: "SlowDown"}},
		"prose where the code stands": {403, `<Error><Code>` + secretBody + `</Code></Error>`, domain.Failure{Class: domain.ClassRejected, Status: 403}},
		"a document that is no error": {400, `<html><Code>NoSuchBucket</Code></html>`, domain.Failure{Class: domain.ClassRejected, Status: 400}},
		"a code nested deeper":        {400, `<Error><Details><Code>NoSuchBucket</Code></Details></Error>`, domain.Failure{Class: domain.ClassRejected, Status: 400}},
		"not XML at all":              {500, secretBody, domain.Failure{Class: domain.ClassStatus, Status: 500}},
		"an empty body":               {500, ``, domain.Failure{Class: domain.ClassStatus, Status: 500}},
	} {
		t.Run(name, func(t *testing.T) {
			failure, failed, text := answered(t, domain.KindObjectStorage, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			if !failed || failure != tc.want {
				t.Errorf("%+v, %t, want %+v", failure, failed, tc.want)
			}
			if strings.Contains(text, "hunter2") {
				t.Errorf("the failure carries the target's text: %q", text)
			}
		})
	}
}

func TestAnS3RedirectIsReadForItsStatusNotItsBody(t *testing.T) {
	failure, failed, _ := answered(t, domain.KindObjectStorage, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMovedPermanently)
		_, _ = w.Write([]byte(`<Error><Code>PermanentRedirect</Code></Error>`))
	})
	if !failed || failure != (domain.Failure{Class: domain.ClassRedirect, Status: 301}) {
		t.Errorf("%+v, %t", failure, failed)
	}
}
