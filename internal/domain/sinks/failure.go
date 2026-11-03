package sinks

import (
	"regexp"
	"strconv"
	"strings"
)

// FailureClass says why a call to a sink's target failed, from a closed list.
// A target's own words are never kept: a ClickHouse or object storage sink may
// reach private addresses, and whoever manages the sink could otherwise read
// an internal service through what it answers with.
type FailureClass string

// The failure classes.
const (
	// ClassDNS is a name that did not resolve.
	ClassDNS FailureClass = "dns"
	// ClassConnect is a target that could not be reached.
	ClassConnect FailureClass = "connect"
	// ClassTLS is a connection that could not be secured.
	ClassTLS FailureClass = "tls"
	// ClassTimeout is a target that did not answer in time.
	ClassTimeout FailureClass = "timeout"
	// ClassRefusedTarget is a target whose address a sink of its kind may not
	// reach; no call was made.
	ClassRefusedTarget FailureClass = "refused_target"
	// ClassRedirect is a target that answered with a redirect, which no sink
	// follows.
	ClassRedirect FailureClass = "redirect"
	// ClassStatus is a target that answered with a status that says it is
	// busy or broken, and may answer otherwise next time.
	ClassStatus FailureClass = "status"
	// ClassRejected is a target that understood the call and refuses it, and
	// will refuse it again.
	ClassRejected FailureClass = "rejected"
)

// FailureClasses are every failure class, in the order the API lists them.
var FailureClasses = []FailureClass{
	ClassDNS, ClassConnect, ClassTLS, ClassTimeout, ClassRefusedTarget, ClassRedirect, ClassStatus, ClassRejected,
}

// Failure is why a call to a target failed: its class, the HTTP status when
// the target answered, and the protocol's own error code when it speaks one.
type Failure struct {
	Class FailureClass
	// Status is the HTTP status the target answered with; zero when it
	// answered nothing.
	Status int
	// Code is the target's own error code; empty when it gave none the
	// protocol defines.
	Code string
}

// Error writes f as its class, then its status and code when it has them.
func (f Failure) Error() string {
	out := string(f.Class)
	if f.Status != 0 {
		out += " " + strconv.Itoa(f.Status)
	}
	if f.Code != "" {
		out += " " + f.Code
	}
	return out
}

// Report is a failure as the API writes it: absent status and code are null.
type Report struct {
	Class  FailureClass
	Status *int
	Code   *string
}

// Report returns f as the API writes it.
func (f Failure) Report() Report {
	r := Report{Class: f.Class}
	if f.Status != 0 {
		r.Status = &f.Status
	}
	if f.Code != "" {
		r.Code = &f.Code
	}
	return r
}

// TransientStatus reports whether an HTTP status says the target is busy or
// broken rather than unwilling: a request timeout, too many requests, or a
// server error.
func TransientStatus(status int) bool {
	return status == 408 || status == 429 || status >= 500
}

// failureOfStatus classifies an answer of status carrying code, and whether it
// is a failure at all. Anything but a 2xx is one: a redirect is followed by no
// sink, a busy or broken target (transient, or refusing on the grounds of code)
// is a status unless refusing, and any other answer is a rejection.
func failureOfStatus(status int, code string, refusing bool) (Failure, bool) {
	switch {
	case status >= 200 && status < 300:
		return Failure{}, false
	case status >= 300 && status < 400:
		return Failure{Class: ClassRedirect, Status: status, Code: code}, true
	case refusing || !TransientStatus(status):
		return Failure{Class: ClassRejected, Status: status, Code: code}, true
	default:
		return Failure{Class: ClassStatus, Status: status, Code: code}, true
	}
}

// WebhookFailure classifies a webhook sink's receiver answering with status:
// a 2xx is no failure, a request timeout, too many requests and a server
// error are a status, and any other answer is a rejection.
func WebhookFailure(status int) (Failure, bool) {
	return failureOfStatus(status, "", false)
}

// ClickHouseFailure classifies ClickHouse answering with status and the value
// of its X-ClickHouse-Exception-Code header. An exception code that says the
// schema or the data does not fit, or the call is not allowed, is a rejection
// whatever the status; any other follows the status, a 5xx carrying a timeout
// or too many parts being transient. The code is written with the exception's
// name when the table knows it ("16 NO_SUCH_COLUMN_IN_TABLE").
func ClickHouseFailure(status int, exceptionCode string) (Failure, bool) {
	code, refusing := clickHouseCode(exceptionCode)
	return failureOfStatus(status, code, refusing)
}

// S3Failure classifies an S3-compatible service answering with status and the
// Code of its XML error document, which SafeS3Code vets. A 2xx is no failure,
// a request timeout, too many requests and a server error are a status, and
// any other answer is a rejection.
func S3Failure(status int, errorCode string) (Failure, bool) {
	return failureOfStatus(status, SafeS3Code(errorCode), false)
}

var s3Code = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)

// SafeS3Code returns an S3 error code as it may be recorded, or nothing: a
// code is a CamelCase word such as NoSuchBucket, and whatever else a target
// writes there is its own words, which are never kept.
func SafeS3Code(code string) string {
	code = strings.TrimSpace(code)
	if s3Code.MatchString(code) {
		return code
	}
	return ""
}

// clickHouseCode returns a value of X-ClickHouse-Exception-Code as it may be
// recorded, and whether the exception is one a retry cannot cure. A value that
// is not a number is nothing.
func clickHouseCode(header string) (code string, refusing bool) {
	n, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || n <= 0 {
		return "", false
	}
	e, known := clickHouseExceptions[n]
	if !known {
		return strconv.Itoa(n), false
	}
	return strconv.Itoa(n) + " " + e.name, e.refuses
}

type clickHouseException struct {
	name string
	// refuses is set for an exception that says the target understood the
	// insert and refuses it: the schema or the data does not fit, or the call
	// is not allowed. Anything else a 5xx carries is the server's trouble.
	refuses bool
}

// clickHouseExceptions are the exceptions a sink meets, by code.
var clickHouseExceptions = map[int]clickHouseException{
	6: {"CANNOT_PARSE_STRING_FROM_TEXT", true}, 16: {"NO_SUCH_COLUMN_IN_TABLE", true},
	26: {"CANNOT_PARSE_QUOTED_STRING", true}, 27: {"CANNOT_PARSE_INPUT_ASSERTION_FAILED", true},
	36: {"BAD_ARGUMENTS", true}, 38: {"CANNOT_PARSE_DATE", true}, 41: {"CANNOT_PARSE_DATETIME", true},
	43: {"ILLEGAL_TYPE_OF_ARGUMENT", true}, 44: {"ILLEGAL_COLUMN", true}, 47: {"UNKNOWN_IDENTIFIER", true},
	53: {"TYPE_MISMATCH", true}, 60: {"UNKNOWN_TABLE", true}, 62: {"SYNTAX_ERROR", true},
	69: {"ARGUMENT_OUT_OF_BOUND", true}, 70: {"CANNOT_CONVERT_TYPE", true}, 72: {"CANNOT_PARSE_NUMBER", true},
	81: {"UNKNOWN_DATABASE", true}, 115: {"UNKNOWN_SETTING", true}, 117: {"INCORRECT_DATA", true},
	192: {"UNKNOWN_USER", true}, 193: {"WRONG_PASSWORD", true}, 467: {"CANNOT_PARSE_BOOL", true},
	497: {"ACCESS_DENIED", true}, 516: {"AUTHENTICATION_FAILED", true},

	159: {"TIMEOUT_EXCEEDED", false}, 164: {"READONLY", false}, 202: {"TOO_MANY_SIMULTANEOUS_QUERIES", false},
	209: {"SOCKET_TIMEOUT", false}, 210: {"NETWORK_ERROR", false}, 241: {"MEMORY_LIMIT_EXCEEDED", false},
	242: {"TABLE_IS_READ_ONLY", false}, 252: {"TOO_MANY_PARTS", false},
}
