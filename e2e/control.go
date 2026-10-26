package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// Killed is the answer of a kill switch.
type Killed struct {
	Flag string `json:"flag"`
	// Changed is false when the flag was off already.
	Changed bool `json:"changed"`
	// CommittedAt is when the configuration switched off was saved, the
	// instant a smoke measures the propagation from.
	CommittedAt time.Time `json:"committed_at"`
}

// Kill switches flag off in the seed environment through replica A.
// Switching off a flag that is off changes nothing.
func Kill(ctx context.Context, s State, flag string) (Killed, error) {
	m, err := SignIn(ctx, s.Seed.Replicas.A)
	if err != nil {
		return Killed{}, err
	}
	res, err := m.KillFlagWithResponse(ctx, ProjectKey, flag, EnvironmentKey, management.Kill{Reason: ptr("end-to-end kill switch")})
	if err != nil {
		return Killed{}, err
	}
	if res.JSON200 == nil {
		return Killed{}, unexpected("kill switch on "+flag, res.StatusCode(), res.Body)
	}
	return Killed{Flag: flag, Changed: res.JSON200.Changed, CommittedAt: res.JSON200.Configuration.UpdatedAt}, nil
}

// RestoreSeed writes the seed's project, segment and flag configurations
// back through replica A, undoing a kill switch, and keeps the keys.
func RestoreSeed(ctx context.Context, s State) error {
	m, err := SignIn(ctx, s.Seed.Replicas.A)
	if err != nil {
		return err
	}
	_, err = Restore(ctx, m)
	return err
}

// Revoke revokes the seed's key of kind (server or client) through replica
// A. Revoking it again changes nothing.
func Revoke(ctx context.Context, s State, kind management.SdkKeyKind) error {
	k, ok := s.Keys[kind]
	if !ok {
		return fmt.Errorf("the seed holds no %q key; kinds are server and client", kind)
	}
	m, err := SignIn(ctx, s.Seed.Replicas.A)
	if err != nil {
		return err
	}
	return revokeKey(ctx, m, k.ID)
}

// Inventory returns the SDK inventory of the seed environment, read
// through replica A.
func Inventory(ctx context.Context, s State) ([]management.SdkInventoryItem, error) {
	m, err := SignIn(ctx, s.Seed.Replicas.A)
	if err != nil {
		return nil, err
	}
	res, err := m.ListSdkInventoryWithResponse(ctx, ProjectKey, EnvironmentKey)
	if err != nil {
		return nil, err
	}
	if res.JSON200 == nil {
		return nil, unexpected("read the SDK inventory", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items, nil
}

// EventVolume returns the events the seed project received per day and
// type, read through replica A. It is how a smoke sees that events it sent
// arrived, a closing page's beacon among them: the counts lag the SDK by
// the rollups' round, ten seconds.
func EventVolume(ctx context.Context, s State) (management.EventVolume, error) {
	m, err := SignIn(ctx, s.Seed.Replicas.A)
	if err != nil {
		return management.EventVolume{}, err
	}
	res, err := m.GetEventVolumeWithResponse(ctx, ProjectKey, &management.GetEventVolumeParams{})
	if err != nil {
		return management.EventVolume{}, err
	}
	if res.JSON200 == nil {
		return management.EventVolume{}, unexpected("read the event volume", res.StatusCode(), res.Body)
	}
	return *res.JSON200, nil
}
