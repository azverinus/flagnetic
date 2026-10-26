package experiments

// The transitions of an experiment that change what the environments
// holding its rule serve, and so the ones a change request proposes.
const (
	// ActionStart starts a draft.
	ActionStart = "start"
	// ActionStop stops a running experiment with a decision, which
	// rewrites every rule serving it.
	ActionStop = "stop"
	// ActionSetTrafficAllocation grows a running experiment's traffic
	// allocation.
	ActionSetTrafficAllocation = "set_traffic_allocation"
)

// Transition is one of those transitions: a start; a stop with Decision;
// or a growth of a running experiment's traffic allocation to
// TrafficAllocation.
type Transition struct {
	Action            string
	Decision          *Decision
	TrafficAllocation *int
}
