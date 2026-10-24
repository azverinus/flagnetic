package experiments

import "github.com/azverinus/flagnetic/internal/domain/experiments"

// UniformityCommand asks for a uniformity check. A nil Salt draws a random
// one in an experiment salt's form; a nil Bins groups the buckets into the
// default 100 bins.
type UniformityCommand struct {
	UnitIDs    []string
	Salt       *string
	SecondSalt *string
	Bins       *int
}

// CheckUniformity runs the uniformity check: the management API and the
// offline command line run this one handler, so both answer alike. It reads
// and records nothing, so unit IDs checked offline never leave the machine.
type CheckUniformity struct{}

// NewCheckUniformity returns the handler.
func NewCheckUniformity() *CheckUniformity { return &CheckUniformity{} }

// Handle checks how evenly the bucketing hash spreads cmd's unit IDs, and
// whether a second salt's buckets are independent of the first's.
func (h *CheckUniformity) Handle(cmd UniformityCommand) (experiments.Uniformity, error) {
	r := experiments.UniformityRequest{UnitIDs: cmd.UnitIDs, SecondSalt: cmd.SecondSalt, Bins: experiments.DefaultUniformityBins}
	if cmd.Bins != nil {
		r.Bins = *cmd.Bins
	}
	if cmd.Salt != nil {
		r.Salt = *cmd.Salt
	} else {
		salt, err := randomSalt()
		if err != nil {
			return experiments.Uniformity{}, err
		}
		r.Salt = salt
	}
	return experiments.CheckUniformity(r)
}
