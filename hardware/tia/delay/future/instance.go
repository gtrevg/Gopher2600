package future

import "fmt"

// Instance represents a single future instance
type Instance struct {
	// the future group this instance belongs to
	group *Group

	// label is a short decription describing the future payload
	label string

	// the number of cycles the instance began with
	InitialCycles int

	// the number of remaining ticks before the pending action is resolved
	RemainingCycles int

	// the value that is to be the result of the pending action
	payload func()

	// arguments to the payload function
	args []interface{}
}

func (ins Instance) String() string {
	return fmt.Sprintf("%s -> %d", ins.label, ins.RemainingCycles)
}

func schedule(group *Group, cycles int, payload func(), label string) *Instance {
	// adjust initial cycles value:
	// + 1 because we trigger the payload on a count of 1 and use zero as the
	// off state
	// + 1 because we'll tick and consume a cycle immediately after scheduling
	cycles += 2
	return &Instance{group: group, label: label, InitialCycles: cycles, RemainingCycles: cycles, payload: payload}
}

func (ins *Instance) tick() bool {
	if ins.RemainingCycles == 1 {
		ins.RemainingCycles--
		ins.payload()
		return true
	}

	if ins.RemainingCycles != 0 {
		ins.RemainingCycles--
	}

	return false
}

// Force can be used to immediately run the future payload
func (ins *Instance) Force() {
	ins.payload()
	ins.group.Force(ins)
}
