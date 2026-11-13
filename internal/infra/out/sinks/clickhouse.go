package sinks

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

// describeLimit is the most of a DESCRIBE answer read: a table's column list.
const describeLimit = 8 << 20

// ClickHouse delivers batches to a ClickHouse server over its HTTP interface.
// Every call it makes, a probe's, a sample's and a batch's, goes through one
// request, so they never drift apart.
type ClickHouse struct{ verify, skipVerify *http.Client }

var _ appsinks.Target = (*ClickHouse)(nil)

// NewClickHouse returns the target calling servers with verify, or with
// skipVerify when a sink asks not to verify the server's certificate.
func NewClickHouse(verify, skipVerify *http.Client) *ClickHouse {
	return &ClickHouse{verify: verify, skipVerify: skipVerify}
}

// chCall is one configured ClickHouse server, as a sink reaches it.
type chCall struct {
	cfg      sinks.ClickHouse
	password string
	client   *http.Client
}

func (c *ClickHouse) call(call appsinks.Call) chCall {
	cfg := *call.Sink.Config.ClickHouse
	client := c.verify
	if cfg.TLSSkipVerify {
		client = c.skipVerify
	}
	password := ""
	if len(call.Secrets) > 0 {
		password = call.Secrets[0]
	}
	return chCall{cfg: cfg, password: password, client: client}
}

// Probe shows whether the server answers a query as the sink's user, and
// whether the tables of the sink's event kinds can take a batch. A missing
// table is created when prepare is set and the sink may create tables;
// otherwise it is reported, as a warning when it would be created, as a failure
// when it may not be. A table lacking a required column is a failure; one
// lacking another column is a warning, and inserts skip that column. Whatever
// is wrong with the tables is named in the warnings, since the failure names
// only a class and a code.
func (c *ClickHouse) Probe(ctx context.Context, call appsinks.Call, _ time.Time, prepare bool) appsinks.Probed {
	ch := c.call(call)
	if _, _, f := ch.do(ctx, url.Values{}, "SELECT 1", nil); f != nil {
		return appsinks.Probed{Failure: f}
	}
	var warnings, problems []string
	var first *sinks.Failure
	problem := func(f sinks.Failure, text string) {
		problems = append(problems, text)
		if first == nil {
			first = &f
		}
	}
	for _, kind := range call.Sink.StreamKinds() {
		t, ok := sinks.ClickHouseTableOf(kind)
		if !ok {
			continue
		}
		name := t.Qualified(ch.cfg.Database, ch.cfg.TablePrefix)
		have, f := ch.describe(ctx, t)
		if f != nil {
			return appsinks.Probed{Failure: f, Warnings: warnings}
		}
		if have == nil {
			switch {
			case !ch.cfg.CreateTables:
				nf := sinks.NoTableFailure()
				problem(nf, fmt.Sprintf("table %s does not exist and create_tables is false", name))
			case !prepare:
				warnings = append(warnings, fmt.Sprintf("table %s does not exist; testing the saved sink or enabling it creates it", name))
			default:
				if cf := ch.create(ctx, t); cf != nil {
					return appsinks.Probed{Failure: cf, Warnings: warnings}
				}
			}
			continue
		}
		if missing := t.MissingRequired(have); len(missing) > 0 {
			problem(sinks.NoColumnFailure(), fmt.Sprintf("table %s lacks the required columns %s", name, strings.Join(missing, ", ")))
			continue
		}
		for _, col := range t.MissingOptional(have) {
			warnings = append(warnings, fmt.Sprintf("column %s is absent from table %s; inserts skip it", col, name))
		}
	}
	if first != nil {
		return appsinks.Probed{Failure: first, Warnings: append(problems, warnings...)}
	}
	return appsinks.Probed{Warnings: warnings}
}

// Deliver inserts the batch into the table of its event kind, the columns the
// export schema and the table share, as gzipped JSONEachRow. The table is
// described before every insert, so a table a data engineer altered is picked
// up without a test; one that is missing is created when the sink may create
// tables. The delivery's ID is the query's ID, and an insert is synchronous, so
// a 200 means the rows are stored. A retry of the same rows is folded into the
// ones already stored by the table's sorting key.
func (c *ClickHouse) Deliver(ctx context.Context, call appsinks.Call, d appsinks.Delivery) *sinks.Failure {
	ch := c.call(call)
	t, ok := sinks.ClickHouseTableOf(d.Kind)
	if !ok {
		return &sinks.Failure{Class: sinks.ClassRejected}
	}
	if len(d.Rows) == 0 {
		return nil
	}
	have, f := ch.describe(ctx, t)
	if f != nil {
		return f
	}
	if have == nil {
		if !ch.cfg.CreateTables {
			nf := sinks.NoTableFailure()
			return &nf
		}
		if cf := ch.create(ctx, t); cf != nil {
			return cf
		}
		have = t.ColumnSet()
	}
	if len(t.MissingRequired(have)) > 0 {
		nf := sinks.NoColumnFailure()
		return &nf
	}
	columns := t.Intersection(have)
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	if err := sinks.WriteJSONEachRow(zw, d.Rows, columns); err != nil {
		return &sinks.Failure{Class: sinks.ClassRejected}
	}
	if err := zw.Close(); err != nil {
		return &sinks.Failure{Class: sinks.ClassConnect}
	}
	params := url.Values{}
	for k, v := range ch.cfg.Settings {
		params.Set(k, v)
	}
	params.Set("query", t.InsertStatement(ch.cfg.Database, ch.cfg.TablePrefix, columns))
	params.Set("query_id", d.ID)
	params.Set("async_insert", "0")
	params.Set("date_time_input_format", "best_effort")
	_, _, f = ch.do(ctx, params, "", body.Bytes())
	return f
}

// describe returns the names of t's columns; nil, with no failure, when the
// table does not exist.
func (c chCall) describe(ctx context.Context, t sinks.ClickHouseTable) (map[string]bool, *sinks.Failure) {
	statement := "DESCRIBE TABLE " + t.Qualified(c.cfg.Database, c.cfg.TablePrefix) + " FORMAT JSONEachRow"
	body, code, f := c.do(ctx, url.Values{}, statement, nil)
	if f != nil {
		if code == sinks.UnknownTableCode {
			return nil, nil
		}
		return nil, f
	}
	columns := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var col struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(sc.Bytes(), &col); err != nil {
			return nil, &sinks.Failure{Class: sinks.ClassRejected}
		}
		columns[col.Name] = true
	}
	if sc.Err() != nil {
		return nil, &sinks.Failure{Class: sinks.ClassRejected}
	}
	return columns, nil
}

func (c chCall) create(ctx context.Context, t sinks.ClickHouseTable) *sinks.Failure {
	_, _, f := c.do(ctx, url.Values{}, t.CreateStatement(c.cfg.Database, c.cfg.TablePrefix), nil)
	return f
}

// do sends one request: statement as the body, or, given gzipped, the query in
// params with gzipped as the compressed body. The password travels only in
// X-ClickHouse-Key, never in the URL. It returns the answer of a 200, and for
// any other answer the exception code ClickHouse gave and the failure; what
// ClickHouse wrote beyond that is dropped.
func (c chCall) do(ctx context.Context, params url.Values, statement string, gzipped []byte) (body []byte, code string, _ *sinks.Failure) {
	u, err := url.Parse(c.cfg.URL)
	if err != nil {
		return nil, "", &sinks.Failure{Class: sinks.ClassConnect}
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	u.RawQuery = params.Encode()
	var reader io.Reader = strings.NewReader(statement)
	if gzipped != nil {
		reader = bytes.NewReader(gzipped)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), reader)
	if err != nil {
		return nil, "", &sinks.Failure{Class: sinks.ClassConnect}
	}
	if gzipped != nil {
		req.Header.Set("Content-Encoding", "gzip")
	}
	req.Header.Set("User-Agent", "Flagnetic-Sink")
	req.Header.Set("X-ClickHouse-User", c.cfg.Username)
	req.Header.Set("X-ClickHouse-Key", c.password)
	res, err := c.client.Do(req)
	if err != nil {
		f := ClassifyError(err)
		return nil, "", &f
	}
	defer func() { _ = res.Body.Close() }()
	code = res.Header.Get("X-ClickHouse-Exception-Code")
	if f, failed := ClassifyResponse(sinks.KindClickHouse, res); failed {
		// What ClickHouse says is read to let the connection be reused, and
		// dropped: no sink keeps a target's words.
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		return nil, strings.TrimSpace(code), &f
	}
	body, err = io.ReadAll(io.LimitReader(res.Body, describeLimit))
	if err != nil {
		f := ClassifyError(err)
		return nil, "", &f
	}
	return body, "", nil
}
