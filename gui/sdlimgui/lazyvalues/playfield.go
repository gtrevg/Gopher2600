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
//
// *** NOTE: all historical versions of this file, as found in any
// git repository, are also covered by the licence, even when this
// notice is not present ***

package lazyvalues

import (
	"gopher2600/hardware/tia/video"
	"sync/atomic"
)

// LazyPlayfield lazily accesses playfield information from the emulator.
type LazyPlayfield struct {
	val *Values

	atomicForegroundColor atomic.Value // uint8
	ForegroundColor       uint8
	atomicBackgroundColor atomic.Value // uint8
	BackgroundColor       uint8
	atomicReflected       atomic.Value // bool
	Reflected             bool
	atomicPriority        atomic.Value // bool
	Priority              bool
	atomicScoremode       atomic.Value // bool
	Scoremode             bool
	atomicRegion          atomic.Value // video.ScreenRegion
	Region                video.ScreenRegion
	atomicPF0             atomic.Value // uint8
	PF0                   uint8
	atomicPF1             atomic.Value // uint8
	PF1                   uint8
	atomicPF2             atomic.Value // uint8
	PF2                   uint8
	atomicIdx             atomic.Value // int
	Idx                   int
	atomicData            atomic.Value // []uint8
	Data                  [20]bool
}

func newLazyPlayfield(val *Values) *LazyPlayfield {
	return &LazyPlayfield{val: val}
}

func (lz *LazyPlayfield) update() {
	lz.val.Dbg.PushRawEvent(func() {
		lz.atomicForegroundColor.Store(lz.val.VCS.TIA.Video.Playfield.ForegroundColor)
		lz.atomicBackgroundColor.Store(lz.val.VCS.TIA.Video.Playfield.BackgroundColor)
		lz.atomicReflected.Store(lz.val.VCS.TIA.Video.Playfield.Reflected)
		lz.atomicPriority.Store(lz.val.VCS.TIA.Video.Playfield.Priority)
		lz.atomicScoremode.Store(lz.val.VCS.TIA.Video.Playfield.Scoremode)
		lz.atomicRegion.Store(lz.val.VCS.TIA.Video.Playfield.Region)
		lz.atomicPF0.Store(lz.val.VCS.TIA.Video.Playfield.PF0)
		lz.atomicPF1.Store(lz.val.VCS.TIA.Video.Playfield.PF1)
		lz.atomicPF2.Store(lz.val.VCS.TIA.Video.Playfield.PF2)
		lz.atomicIdx.Store(lz.val.VCS.TIA.Video.Playfield.Idx)
		lz.atomicData.Store(lz.val.VCS.TIA.Video.Playfield.Data)
	})
	lz.ForegroundColor, _ = lz.atomicForegroundColor.Load().(uint8)
	lz.BackgroundColor, _ = lz.atomicBackgroundColor.Load().(uint8)
	lz.Reflected, _ = lz.atomicReflected.Load().(bool)
	lz.Priority, _ = lz.atomicPriority.Load().(bool)
	lz.Scoremode, _ = lz.atomicScoremode.Load().(bool)
	lz.Region, _ = lz.atomicRegion.Load().(video.ScreenRegion)
	lz.PF0, _ = lz.atomicPF0.Load().(uint8)
	lz.PF1, _ = lz.atomicPF1.Load().(uint8)
	lz.PF2, _ = lz.atomicPF2.Load().(uint8)
	lz.Idx, _ = lz.atomicIdx.Load().(int)
	lz.Data, _ = lz.atomicData.Load().([20]bool)
}
