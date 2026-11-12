package transfer

import (
	"context"
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// deleteAbsent deletes what the project has that the document does not name,
// after the document is merged: experiments, flags, segments, attributes,
// unit types, then environments. Each is deleted through the context that
// owns it, which refuses what something still uses; every such refusal is
// collected, so the answer names all that is in use, and nothing is deleted
// for good unless all can be, since the refusal rolls the whole import back.
// An absent experiment that is not a draft ends the import at once, naming
// every one.
func (r *mergeRun) deleteAbsent(ctx context.Context) error {
	del := transfer.PlanDeletions(r.current, r.eff)
	if err := del.Refusal(); err != nil {
		return err
	}
	used := slices.Clone(del.Held)
	// refused stays true once a deletion is refused for being in use, so that
	// the import is refused even were a refusal to name no reference.
	refused := len(used) > 0
	// remove deletes the entity keyed key, noting a refusal for being in use
	// and returning any other error, which ends the import.
	remove := func(key string, f func() error, deleted *[]string) error {
		err := f()
		if refs, inUse := inUseReferences(err); inUse {
			used = append(used, refs...)
			refused = true
			return nil
		}
		if err != nil {
			return err
		}
		*deleted = append(*deleted, key)
		r.wrote = true
		return nil
	}
	held := map[string]bool{}
	for _, e := range r.eff.Experiments {
		if slices.Contains(del.Flags, e.Flag) {
			held[e.Flag] = true
		}
	}
	d := r.h.d
	for _, key := range del.Experiments {
		if err := remove(key, func() error { return d.ExperimentsImport.DeleteExperiment(ctx, r.project.ID, key, r.req) },
			&r.changes.Experiments.Deleted); err != nil {
			return err
		}
	}
	for _, key := range del.Flags {
		// A flag an experiment of the document holds is refused already: the
		// flag would take the experiment with it.
		if held[key] {
			continue
		}
		if err := remove(key, func() error {
			effect, err := d.FlagsImport.DeleteFlag(ctx, r.project.ID, key, r.req)
			r.took(effect)
			return err
		}, &r.changes.Flags.Deleted); err != nil {
			return err
		}
	}
	for _, key := range del.Segments {
		if err := remove(key, func() error {
			effect, err := d.FlagsImport.DeleteSegment(ctx, r.project.ID, key, r.req)
			r.took(effect)
			return err
		}, &r.changes.Segments.Deleted); err != nil {
			return err
		}
	}
	for _, key := range del.Attributes {
		if err := remove(key, func() error { return d.WorkspaceImport.DeleteAttribute(ctx, r.project.ID, key, r.req) },
			&r.changes.Attributes.Deleted); err != nil {
			return err
		}
		r.allMoved = true
	}
	for _, key := range del.UnitTypes {
		if err := remove(key, func() error { return d.WorkspaceImport.DeleteUnitType(ctx, r.project.ID, key, r.req) },
			&r.changes.UnitTypes.Deleted); err != nil {
			return err
		}
		r.allMoved = true
	}
	for _, key := range del.Environments {
		env := r.environments[key]
		keys, err := d.SDKKeys.LiveSDKKeys(ctx, env.ID)
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			refused = true
			for _, k := range keys {
				used = append(used, flags.Reference{Kind: "sdk_key", Key: k, Environment: key})
			}
			continue
		}
		if err := remove(key, func() error { return d.WorkspaceImport.DeleteEnvironment(ctx, r.project.ID, key, r.req) },
			&r.changes.Environments.Deleted); err != nil {
			return err
		}
		delete(r.environments, key)
		delete(r.moved, key)
	}
	if refused {
		return flags.ErrInUse(used)
	}
	return nil
}

// inUseReferences returns the references an in_use refusal lists, and whether
// err is one.
func inUseReferences(err error) ([]flags.Reference, bool) {
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindConflict || e.Code != "in_use" {
		return nil, false
	}
	members, _ := e.Extensions["references"].([]map[string]any)
	refs := make([]flags.Reference, 0, len(members))
	for _, m := range members {
		ref := flags.Reference{}
		ref.Kind, _ = m["kind"].(string)
		ref.Key, _ = m["key"].(string)
		ref.Environment, _ = m["environment"].(string)
		refs = append(refs, ref)
	}
	return refs, true
}
