package flags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// configurationDoc is a configuration's document as the flag_configurations
// table stores it: what the configuration serves, read and written whole.
// Its rules, conditions and prerequisites have the shape a ruleset carries
// them in, which is what the reverse reference queries search.
type configurationDoc struct {
	On            bool              `json:"on"`
	OffVariation  string            `json:"off_variation"`
	Fallthrough   string            `json:"fallthrough"`
	Prerequisites []prerequisiteDoc `json:"prerequisites"`
	Overrides     []overrideDoc     `json:"overrides"`
	Rules         []ruleDoc         `json:"rules"`
}

type prerequisiteDoc struct {
	Flag      string `json:"flag"`
	Variation string `json:"variation"`
}

type overrideDoc struct {
	UnitType  string           `json:"unit_type"`
	UnitID    string           `json:"unit_id"`
	Variation string           `json:"variation"`
	Note      string           `json:"note,omitempty"`
	AddedBy   *access.MemberID `json:"added_by,omitempty"`
}

type ruleDoc struct {
	ID          flags.RuleID   `json:"id"`
	Description string         `json:"description,omitempty"`
	Conditions  []conditionDoc `json:"conditions"`
	Serve       serveDoc       `json:"serve"`
}

type serveDoc struct {
	Kind       string `json:"kind"`
	Variation  string `json:"variation,omitempty"`
	UnitType   string `json:"unit_type,omitempty"`
	Percentage *int   `json:"percentage,omitempty"`
	Salt       string `json:"salt,omitempty"`
}

func docOf(c flags.Configuration) ([]byte, error) {
	d := configurationDoc{
		On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough,
		Prerequisites: make([]prerequisiteDoc, len(c.Prerequisites)), Overrides: make([]overrideDoc, len(c.Overrides)),
		Rules: make([]ruleDoc, len(c.Rules)),
	}
	for i, p := range c.Prerequisites {
		d.Prerequisites[i] = prerequisiteDoc(p)
	}
	for i, o := range c.Overrides {
		d.Overrides[i] = overrideDoc(o)
	}
	for i, r := range c.Rules {
		conditions := make([]conditionDoc, len(r.Conditions))
		for j, cond := range r.Conditions {
			conditions[j] = conditionDoc(cond)
		}
		serve := serveDoc{Kind: r.Serve.Kind, Variation: r.Serve.Variation, UnitType: r.Serve.UnitType, Salt: r.Serve.Salt}
		if r.Serve.Kind == flags.ServeRollout {
			serve.Percentage = &r.Serve.Percentage
		}
		d.Rules[i] = ruleDoc{ID: r.ID, Description: r.Description, Conditions: conditions, Serve: serve}
	}
	return json.Marshal(d)
}

// fromDoc sets what c serves from the document doc.
func fromDoc(doc []byte, c *flags.Configuration) error {
	var d configurationDoc
	if err := json.Unmarshal(doc, &d); err != nil {
		return fmt.Errorf("decoding a configuration: %w", err)
	}
	c.On, c.OffVariation, c.Fallthrough = d.On, d.OffVariation, d.Fallthrough
	c.Prerequisites = make([]flags.Prerequisite, len(d.Prerequisites))
	for i, p := range d.Prerequisites {
		c.Prerequisites[i] = flags.Prerequisite(p)
	}
	c.Overrides = make([]flags.Override, len(d.Overrides))
	for i, o := range d.Overrides {
		c.Overrides[i] = flags.Override(o)
	}
	c.Rules = make([]flags.Rule, len(d.Rules))
	for i, r := range d.Rules {
		conditions := make([]flags.Condition, len(r.Conditions))
		for j, cond := range r.Conditions {
			conditions[j] = flags.Condition(cond)
		}
		serve := flags.Serve{Kind: r.Serve.Kind, Variation: r.Serve.Variation, UnitType: r.Serve.UnitType, Salt: r.Serve.Salt}
		if r.Serve.Percentage != nil {
			serve.Percentage = *r.Serve.Percentage
		}
		c.Rules[i] = flags.Rule{ID: r.ID, Description: r.Description, Conditions: conditions, Serve: serve}
	}
	return nil
}

// CreateConfigurations stores cs.
func (s *Store) CreateConfigurations(ctx context.Context, cs []flags.Configuration) error {
	b := &pgx.Batch{}
	for _, c := range cs {
		doc, err := docOf(c)
		if err != nil {
			return err
		}
		b.Queue(`INSERT INTO flag_configurations (flag_id, environment_id, document, updated_at, updated_by, version)
			VALUES ($1, $2, $3, $4, $5, $6)`, c.Flag.Bytes(), c.Environment.Bytes(), doc, c.UpdatedAt,
			optionalMember(c.UpdatedBy), c.Version)
	}
	return s.db(ctx).SendBatch(ctx, b).Close()
}

// UpdateConfiguration stores c's changes.
func (s *Store) UpdateConfiguration(ctx context.Context, c flags.Configuration) error {
	doc, err := docOf(c)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		UPDATE flag_configurations SET document = $3, updated_at = $4, updated_by = $5, version = $6
		WHERE flag_id = $1 AND environment_id = $2`,
		c.Flag.Bytes(), c.Environment.Bytes(), doc, c.UpdatedAt, optionalMember(c.UpdatedBy), c.Version)
	return err
}

const configurationSelect = `
	SELECT c.flag_id, e.key, c.environment_id, c.document, c.updated_at, c.updated_by, c.version
	FROM flag_configurations c JOIN environments e ON e.id = c.environment_id`

func scanConfiguration(row pgx.Row) (flags.EnvironmentConfiguration, error) {
	var (
		ec        flags.EnvironmentConfiguration
		fk, ek    [16]byte
		doc       []byte
		updatedBy *[16]byte
	)
	c := &ec.Configuration
	if err := row.Scan(&fk, &ec.Environment, &ek, &doc, &c.UpdatedAt, &updatedBy, &c.Version); err != nil {
		return ec, err
	}
	c.Flag, c.Environment, c.UpdatedAt = id.FromBytes[flags.Flag](fk), id.FromBytes[workspace.Environment](ek), c.UpdatedAt.UTC()
	if updatedBy != nil {
		m := id.FromBytes[access.Member](*updatedBy)
		c.UpdatedBy = &m
	}
	return ec, fromDoc(doc, c)
}

// Configurations returns flag's configuration in every environment, by the
// environments' order.
func (s *Store) Configurations(ctx context.Context, flag flags.FlagID) ([]flags.EnvironmentConfiguration, error) {
	rows, err := s.db(ctx).Query(ctx, configurationSelect+`
		WHERE c.flag_id = $1
		ORDER BY e.sort_order, e.key COLLATE "C"`, flag.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading configurations: %w", err)
	}
	defer rows.Close()
	out := []flags.EnvironmentConfiguration{}
	for rows.Next() {
		ec, err := scanConfiguration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ec)
	}
	return out, rows.Err()
}

// EnvironmentConfigurations returns the configuration of every flag of
// environment's project there, archived flags included.
func (s *Store) EnvironmentConfigurations(ctx context.Context, environment workspace.EnvironmentID) ([]flags.Configuration, error) {
	rows, err := s.db(ctx).Query(ctx, configurationSelect+`
		WHERE c.environment_id = $1`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading an environment's configurations: %w", err)
	}
	defer rows.Close()
	out := []flags.Configuration{}
	for rows.Next() {
		ec, err := scanConfiguration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ec.Configuration)
	}
	return out, rows.Err()
}

// Configuration returns flag's configuration in the environment keyed
// environment, or appflags.ErrNotFound when its project has no such
// environment.
func (s *Store) Configuration(ctx context.Context, flag flags.FlagID, environment string) (flags.EnvironmentConfiguration, error) {
	ec, err := scanConfiguration(s.db(ctx).QueryRow(ctx, configurationSelect+`
		WHERE c.flag_id = $1 AND e.key = $2`, flag.Bytes(), environment))
	if errors.Is(err, pgx.ErrNoRows) {
		return ec, appflags.ErrNotFound
	}
	if err != nil {
		return ec, fmt.Errorf("reading a configuration: %w", err)
	}
	return ec, nil
}

// Requirements returns, by flag key, the keys of the flags each flag's
// configuration in environment requires, through its prerequisites and its
// rules' prerequisite conditions, archived flags included, each in byte
// order and once.
func (s *Store) Requirements(ctx context.Context, environment workspace.EnvironmentID) (flags.Requirements, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT f.key, r.flag
		FROM flag_configurations c
		JOIN flags f ON f.id = c.flag_id
		CROSS JOIN LATERAL (
		    SELECT p->>'flag' AS flag FROM jsonb_array_elements(c.document->'prerequisites') p
		    UNION
		    SELECT cond->>'flag' FROM jsonb_array_elements(c.document->'rules') rule,
		        jsonb_array_elements(rule->'conditions') cond
		    WHERE cond->>'kind' = 'prerequisite'
		) r
		WHERE c.environment_id = $1
		ORDER BY f.key COLLATE "C", r.flag COLLATE "C"`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading requirements: %w", err)
	}
	defer rows.Close()
	out := flags.Requirements{}
	for rows.Next() {
		var flag, required string
		if err := rows.Scan(&flag, &required); err != nil {
			return nil, err
		}
		out[flag] = append(out[flag], required)
	}
	return out, rows.Err()
}

// Dependents returns every flag of project whose configuration in an
// environment requires a variation of the flag keyed flag, archived flags
// included, once per variation, by flag key, then environment order, then
// variation. Containment of the flag key finds both kinds of requirement
// through the configurations' GIN index.
func (s *Store) Dependents(ctx context.Context, project workspace.ProjectID, flag string) ([]flags.Dependent, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT f.key, e.key, r.variation
		FROM flag_configurations c
		JOIN flags f ON f.id = c.flag_id
		JOIN environments e ON e.id = c.environment_id
		CROSS JOIN LATERAL (
		    SELECT p->>'variation' AS variation FROM jsonb_array_elements(c.document->'prerequisites') p
		    WHERE p->>'flag' = $2
		    UNION
		    SELECT cond->>'variation' FROM jsonb_array_elements(c.document->'rules') rule,
		        jsonb_array_elements(rule->'conditions') cond
		    WHERE cond->>'kind' = 'prerequisite' AND cond->>'flag' = $2
		) r
		WHERE f.project_id = $1
		  AND (c.document @> jsonb_build_object('prerequisites', jsonb_build_array(jsonb_build_object('flag', $2::text)))
		    OR c.document @> jsonb_build_object('rules', jsonb_build_array(jsonb_build_object('conditions',
		        jsonb_build_array(jsonb_build_object('kind', 'prerequisite', 'flag', $2::text))))))
		ORDER BY f.key COLLATE "C", e.sort_order, e.key COLLATE "C", r.variation COLLATE "C"`, project.Bytes(), flag)
	if err != nil {
		return nil, fmt.Errorf("finding the flags requiring a flag: %w", err)
	}
	defer rows.Close()
	out := []flags.Dependent{}
	for rows.Next() {
		var d flags.Dependent
		if err := rows.Scan(&d.Flag, &d.Environment, &d.Variation); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
