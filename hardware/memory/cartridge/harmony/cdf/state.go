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

package cdf

type State struct {
	// currently selected bank
	bank int

	// keeps track of LDA and JMP triggers when fast fetch mode is active
	fastLDA bool
	fastJMP int

	// music fetchers are clocked at a fixed (slower) rate than the reference
	// to the VCS's clock. see Step() function.
	beats int

	registers Registers

	// static area of the cartridge. accessible outside of the cartridge
	// through GetStatic() and PutStatic()
	static *Static
}

// initialise should be called as soon as convenient.
func newCDFstate() *State {
	s := &State{}
	return s
}

func (s *State) initialise(bank int) {
	s.bank = bank
	s.fastLDA = false
	s.fastJMP = 0
	s.beats = 0
	s.registers.initialise()
}

func (s *State) Snapshot() *State {
	n := *s
	n.static = s.static.Snapshot()
	return &n
}