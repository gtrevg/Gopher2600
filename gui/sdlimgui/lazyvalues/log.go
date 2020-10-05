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

package lazyvalues

import (
	"sync/atomic"

	"github.com/jetsetilly/gopher2600/logger"
)

// LazyLog lazily accesses chip registere information from the emulator
type LazyLog struct {
	val *Lazy

	atomicLog   atomic.Value // []logger.Entry
	atomicDirty atomic.Value // bool
	Log         []logger.Entry
	Dirty       bool
}

func newLazyLog(val *Lazy) *LazyLog {
	return &LazyLog{val: val}
}

func (lz *LazyLog) update() {
	lz.val.Dbg.PushRawEvent(func() {
		if l := logger.Copy(); l != nil {
			lz.atomicLog.Store(l)
			lz.atomicDirty.Store(true)
		} else {
			lz.atomicDirty.Store(false)
		}
	})

	if l, ok := lz.atomicLog.Load().([]logger.Entry); ok {
		lz.Log = l
		if lz.atomicDirty.Load().(bool) {
			lz.Dirty = true
		}
	}
}