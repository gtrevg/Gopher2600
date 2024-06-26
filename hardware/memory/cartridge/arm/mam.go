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

package arm

import (
	"github.com/jetsetilly/gopher2600/environment"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/arm/architecture"
	"github.com/jetsetilly/gopher2600/hardware/preferences"
	"github.com/jetsetilly/gopher2600/logger"
)

// MAM implements the memory addressing module as found in the LPC20000. Not
// fully implemented but good enough for most Harmony games
type mam struct {
	env  *environment.Environment
	mmap architecture.Map

	// valid values for mamcr are 0, 1 or 2 are valid. we can think of these
	// respectively, as "disable", "partial" and "full"
	mamcr architecture.MAMCR

	// NOTE: not used yet
	mamtim uint32

	// the preference value
	pref int

	// the address of the last prefetch and data read
	prefectchLatch uint32
	dataLatch      uint32

	// the address of the last branch. implements the branch trail buffer.
	// if an unexpected PC value is the same as lastBranchAddress then there is
	// no need to fetch from flash
	branchLatch uint32

	// if the previous cycle was a data read any pending MAM prefetch will be
	// aborted causing a MAM miss
	prefectchAborted bool
}

func newMam(env *environment.Environment, mmap architecture.Map) mam {
	return mam{
		env:  env,
		mmap: mmap,
	}
}

func (m *mam) Plumb(env *environment.Environment) {
	m.env = env
}

func (m *mam) updatePrefs() {
	m.pref = m.env.Prefs.ARM.MAM.Get().(int)
	if m.pref == preferences.MAMDriver {
		m.mamcr = m.mmap.PreferredMAMCR
		m.mamtim = 4.0
	} else {
		m.setMAMCR(architecture.MAMCR(m.pref))
		m.mamtim = 4.0
	}
}

func (m *mam) Write(addr uint32, val uint32) bool {
	switch addr {
	case m.mmap.MAMCR:
		if m.pref == preferences.MAMDriver {
			m.setMAMCR(architecture.MAMCR(val))
		}
	case m.mmap.MAMTIM:
		if m.pref == preferences.MAMDriver {
			if m.mamcr == 0 {
				m.mamtim = val
			} else {
				logger.Logf(m.env, "ARM7", "trying to write to MAMTIM while MAMCR is active")
			}
		}
	default:
		return false
	}

	return true
}

func (m *mam) Read(addr uint32) (uint32, bool) {
	var val uint32

	switch addr {
	case m.mmap.MAMCR:
		val = uint32(m.mamcr)
	case m.mmap.MAMTIM:
		val = m.mamtim
	default:
		return 0, false
	}

	return val, true
}

func (m *mam) setMAMCR(val architecture.MAMCR) {
	m.mamcr = val
	if m.mamcr > 2 {
		logger.Logf(m.env, "ARM7", "setting MAMCR to a value greater than 2 (%#08x)", m.mamcr)
	}
}
