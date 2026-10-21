package flags

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
)

// stateDoc is a flag state as the list query aggregates it.
type stateDoc struct {
	Environment string `json:"environment"`
	On          bool   `json:"on"`
	HasRules    bool   `json:"has_rules"`
	HasRollout  bool   `json:"has_rollout"`
}

// rolloutRule is the containment a configuration whose rules serve a
// rollout matches, through the configurations' GIN index.
const rolloutRule = `'{"rules": [{"serve": {"kind": "rollout"}}]}'`

// stateConditions are what a configuration c in the state's environment
// holds when the flag is in that state.
var stateConditions = map[appflags.StateFilter]string{
	appflags.StateOn:      `(c.document->>'on')::boolean`,
	appflags.StateOff:     `NOT (c.document->>'on')::boolean`,
	appflags.StateRules:   `jsonb_array_length(c.document->'rules') > 0`,
	appflags.StateRollout: `c.document @> ` + rolloutRule,
}

// flagOrders are the ORDER BY of each sort, and the keyset condition of a
// page after cursor c, its values passed through arg. A key or name
// compares in byte order, as the console shows them; the key breaks ties,
// as it is unique in the project.
var flagOrders = map[appflags.FlagSort]struct {
	order string
	after func(c appflags.FlagCursor, arg func(any) string) string
}{
	appflags.SortByKey: {`f.key COLLATE "C"`, func(c appflags.FlagCursor, arg func(any) string) string {
		return `f.key COLLATE "C" > ` + arg(c.Key)
	}},
	appflags.SortByName: {`f.name COLLATE "C", f.key COLLATE "C"`, func(c appflags.FlagCursor, arg func(any) string) string {
		return `(f.name COLLATE "C", f.key COLLATE "C") > (` + arg(c.Name) + `, ` + arg(c.Key) + `)`
	}},
	appflags.SortByUpdated: {`f.updated_at DESC, f.key COLLATE "C" DESC`, func(c appflags.FlagCursor, arg func(any) string) string {
		return `(f.updated_at, f.key COLLATE "C") < (` + arg(c.UpdatedAt) + `, ` + arg(c.Key) + `)`
	}},
}

// likeEscaper makes text match itself in a LIKE pattern.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Flags calls each with the flags q lists, in q's order, each with its
// state in every environment and, when q compares two environments, its
// configurations in both, until each answers false or the flags run out,
// all in one query. A comparison reads only the flags whose two documents
// differ: equal documents carry the same ruleset, and what else differs is
// judged by the caller.
func (s *Store) Flags(ctx context.Context, q appflags.FlagQuery, each func(appflags.QueriedFlag) bool) error {
	sql, args := flagsQuery(q)
	rows, err := s.db(ctx).Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("listing flags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		f, err := scanQueriedFlag(rows, q.Compared != nil)
		if err != nil {
			return err
		}
		if !each(f) {
			return nil
		}
	}
	return rows.Err()
}

// flagsQuery returns the SQL listing what q asks for, and its arguments.
func flagsQuery(q appflags.FlagQuery) (string, []any) {
	args := []any{q.Project.Bytes(), q.Archived}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	var sql strings.Builder
	sql.WriteString(`SELECT ` + prefixed("f.", flagColumns) + `, st.states`)
	if q.Compared != nil {
		sql.WriteString(`, a.document, b.document`)
	}
	sql.WriteString(`
		FROM flags f
		CROSS JOIN LATERAL (
		    SELECT jsonb_agg(jsonb_build_object(
		        'environment', e.key, 'on', c.document->'on',
		        'has_rules', jsonb_array_length(c.document->'rules') > 0,
		        'has_rollout', c.document @> ` + rolloutRule + `)
		        ORDER BY e.sort_order, e.key COLLATE "C")
		    FROM flag_configurations c JOIN environments e ON e.id = c.environment_id
		    WHERE c.flag_id = f.id
		) st(states)`)
	if q.Compared != nil {
		sql.WriteString(`
		JOIN flag_configurations a ON a.flag_id = f.id AND a.environment_id = ` + arg(q.Compared[0].Bytes()) + `
		JOIN flag_configurations b ON b.flag_id = f.id AND b.environment_id = ` + arg(q.Compared[1].Bytes()))
	}
	sql.WriteString(`
		WHERE f.project_id = $1 AND (f.archived_at IS NOT NULL) = $2`)
	if q.Text != "" {
		pattern := arg("%" + likeEscaper.Replace(q.Text) + "%")
		sql.WriteString(` AND (f.key ILIKE ` + pattern + ` OR f.name ILIKE ` + pattern + `)`)
	}
	if q.Type != "" {
		sql.WriteString(` AND f.type = ` + arg(string(q.Type)))
	}
	if q.Tag != "" {
		sql.WriteString(` AND f.tags @> ARRAY[` + arg(q.Tag) + `::text]`)
	}
	if q.Owner != nil {
		sql.WriteString(` AND f.owner_id = ` + arg(q.Owner.Bytes()))
	}
	if q.In != nil {
		sql.WriteString(` AND EXISTS (SELECT 1 FROM flag_configurations c WHERE c.flag_id = f.id AND c.environment_id = ` +
			arg(q.In.Bytes()) + ` AND ` + stateConditions[q.State] + `)`)
	}
	if q.Compared != nil {
		sql.WriteString(` AND a.document <> b.document`)
	}
	order := flagOrders[q.Sort]
	if q.After != nil {
		sql.WriteString(` AND ` + order.after(*q.After, arg))
	}
	sql.WriteString(`
		ORDER BY ` + order.order)
	if q.Limit > 0 {
		sql.WriteString(` LIMIT ` + arg(q.Limit))
	}
	return sql.String(), args
}

// prefixed returns the comma-separated columns each prefixed by prefix.
func prefixed(prefix, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}

// scanQueriedFlag reads a row of the list query, with the two compared
// configurations' documents when compared is set.
func scanQueriedFlag(row pgx.Row, compared bool) (appflags.QueriedFlag, error) {
	var (
		out        appflags.QueriedFlag
		states     []byte
		docA, docB []byte
	)
	dest := []any{&states}
	if compared {
		dest = append(dest, &docA, &docB)
	}
	f, err := scanFlag(row, dest...)
	if err != nil {
		return out, err
	}
	out.Flag = f
	var docs []stateDoc
	if err := json.Unmarshal(states, &docs); err != nil {
		return out, fmt.Errorf("decoding flag states: %w", err)
	}
	out.States = make([]appflags.FlagState, len(docs))
	for i, d := range docs {
		out.States[i] = appflags.FlagState(d)
	}
	if compared {
		if err := fromDoc(docA, &out.Compared[0]); err != nil {
			return out, err
		}
		if err := fromDoc(docB, &out.Compared[1]); err != nil {
			return out, err
		}
	}
	return out, nil
}
