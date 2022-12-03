// This file is part of Gopher2600.
//
// Gopher2600 is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Gopher2600 is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with Gopher2600.  If not, see <https://www.gnu.org/licenses/>.

package developer

import (
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
)

type YieldedLocal struct {
	*SourceVariableLocal

	// whether this specific local variable is in resolvable range. when this
	// value is true, results from Address() and Value() can be used
	IsResolving bool

	// whether the location of the variable can ever be resolved
	CanResolve bool

	// whether the location cannot be found because of an error
	ErrorOnResolve bool
}

// YieldState records the most recent yield.
type YieldState struct {
	InstructionPC   uint32
	InstructionLine *SourceLine
	Reason          mapper.YieldReason

	LocalVariables []*YieldedLocal
}

// Cmp returns true if two YieldStates are equal.
func (y *YieldState) Cmp(w *YieldState) bool {
	return y.InstructionPC == w.InstructionPC && y.Reason == w.Reason
}

// OnYield implements the mapper.CartCoProcDeveloper interface.
func (dev *Developer) OnYield(instructionPC uint32, reason mapper.YieldReason) {
	var ln *SourceLine
	var locals []*YieldedLocal

	// using BorrowSource because we want to make sure the source lock is
	// released if there is an error and the code panics
	dev.BorrowSource(func(src *Source) {
		// make sure that src is valid
		if src == nil {
			return
		}

		ln = src.FindSourceLine(instructionPC)
		if ln == nil {
			return
		}

		// log a bug for any of these reasons
		switch reason {
		case mapper.YieldMemoryAccessError:
			fallthrough
		case mapper.YieldExecutionError:
			fallthrough
		case mapper.YieldUnimplementedFeature:
			fallthrough
		case mapper.YieldUndefinedBehaviour:
			if src != nil {
				if ln != nil {
					ln.Bug = true
				}
			}
		}

		// match local variables for any reason other than VCS synchronisation
		//
		// yielding for this reason is likely to be followed by another yield
		// very soon after so there is no point garthing this information
		if reason != mapper.YieldSyncWithVCS {
			// there's an assumption here that SortedLocals is sorted by variable name
			var prev string

			var candidateFound bool
			var candidates []*SourceVariableLocal
			processCandidates := func() {
				if !candidateFound {
					if len(candidates) > 0 {
						l := &YieldedLocal{
							SourceVariableLocal: candidates[0],
							IsResolving:         false && candidates[0].errorOnResolve == nil,
							CanResolve:          candidates[0].loclist != nil && candidates[0].errorOnResolve == nil,
							ErrorOnResolve:      candidates[0].errorOnResolve != nil,
						}
						locals = append(locals, l)
					}
				}
				candidateFound = false
				candidates = candidates[:0]
			}

			var id string
			for _, local := range src.SortedLocals.Locals {
				inFunction, resolving := local.find(ln)
				if inFunction {
					id = local.id()
					if prev != id {
						processCandidates()
					}
					prev = id

					if candidateFound {
						continue
					}

					if resolving {
						l := &YieldedLocal{
							SourceVariableLocal: local,
							IsResolving:         true && local.errorOnResolve == nil,
							CanResolve:          local.loclist != nil && local.errorOnResolve == nil,
							ErrorOnResolve:      local.errorOnResolve != nil,
						}
						locals = append(locals, l)
						candidateFound = true
					} else {
						candidates = append(candidates, local)
					}
				} else if resolving {
					// this shouldn't be possible. if this happens then
					// something has gone wrong with the DWARF parsing
				}
			}

			processCandidates()
		}

		// update all globals (locals are updated below)
		src.UpdateGlobalVariables()
	})

	dev.yieldStateLock.Lock()
	defer dev.yieldStateLock.Unlock()

	dev.yieldState.InstructionPC = instructionPC
	dev.yieldState.InstructionLine = ln
	dev.yieldState.Reason = reason

	// clear list of local variables from previous yield and extend with new
	// list of locals
	dev.yieldState.LocalVariables = dev.yieldState.LocalVariables[:0]
	dev.yieldState.LocalVariables = append(dev.yieldState.LocalVariables, locals...)

	// update all locals (globals are updated above)
	for _, local := range dev.yieldState.LocalVariables {
		local.update()
	}
}

// BorrowYieldState will lock the illegal access log for the duration of the
// supplied fucntion, which will be executed with the illegal access log as an
// argument.
func (dev *Developer) BorrowYieldState(f func(*YieldState)) {
	dev.yieldStateLock.Lock()
	defer dev.yieldStateLock.Unlock()
	f(&dev.yieldState)
}
