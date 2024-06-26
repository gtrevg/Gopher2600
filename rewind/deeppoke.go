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

package rewind

import "fmt"

type PokeHook func(res *State) error

// RunPoke will the run the VCS from one state to another state applying
// the supplied PokeHook to the from State
func (r *Rewind) RunPoke(from *State, to *State, poke PokeHook) error {
	fromIdx := r.findFrameIndex(from.TV.GetCoords().Frame).nearestIdx

	if poke != nil {
		err := poke(r.entries[fromIdx])
		if err != nil {
			return err
		}
	}

	err := r.setSplicePoint(fromIdx, to.TV.GetCoords(), nil)
	if err != nil {
		return fmt.Errorf("rewind: %w", err)
	}

	return nil
}
