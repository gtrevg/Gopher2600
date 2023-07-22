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

package profiling

// Load records the frame (or current) load as well as the average and
// maximum load.
type Load struct {
	// cycle count
	FrameCount   float32
	AverageCount float32
	MaxCount     float32

	// cycle count expressed as a percentage
	Frame   float32
	Average float32
	Max     float32

	// whether the corresponding values are valid
	FrameValid   bool
	AverageValid bool
	MaxValid     bool
}

func (ld *Load) reset() {
	ld.FrameCount = 0.0
	ld.AverageCount = 0.0
	ld.MaxCount = 0.0
	ld.Frame = 0.0
	ld.Average = 0.0
	ld.Max = 0.0
	ld.FrameValid = false
	ld.AverageValid = false
	ld.MaxValid = false
}

// StatsGroup collates the Stats instance for all kernel views of the program.
type StatsGroup struct {
	// cycle statistics for the entire program
	Overall Stats

	// kernel specific cycle statistics for the program. accumulated only once TV is stable
	VBLANK   Stats
	Screen   Stats
	Overscan Stats
	ROMSetup Stats
}

// Stats records the cycle count over time and can be used to the frame
// (or current) load as well as average and maximum load.
//
// The actual percentage values are accessed through the OverSource and
// OverFunction fields. These fields provide the necessary scale by which
// the load is measured.
//
// The validity of the OverSource and OverFunction fields depends on context.
// For instance, for the SourceFunction type, the corresponding OverFunction
// field is invalid. For the Source type meanwhile, neither field is valid.
//
// For the SourceLine type however, both OverSource and OverFunction can be
// used to provide a different scaling to the load values.
type Stats struct {
	OverSource   Load
	OverFunction Load

	// cycle count this frame
	Count float32

	// cycle count over all frames
	allFrameCount float32

	// number of frames seen
	numFrames float32

	// frame and average count form the basis of the frame, average and max
	// counts (and percentages) in the Load type
	frameCount float32
	avgCount   float32
}

// HasExecuted returns true if the statistics have ever been updated. ie. the
// source associated with this statistic has ever executed.
//
// Not to be confused with the FrameValid, AverageValid and MaxValid fields of
// the Load type.
func (stats *Stats) HasExecuted() bool {
	return stats.allFrameCount > 0
}

// NewFrame update statistics, using source and function to update the Load values as
// appropriate.
func (stats *Stats) NewFrame(src *Stats, function *Stats) {
	stats.numFrames++
	if stats.numFrames > 1 {
		if stats.Count > 0 {
			stats.allFrameCount += stats.Count
			stats.avgCount = stats.allFrameCount / (stats.numFrames - 1)
		}
	}

	stats.frameCount = stats.Count
	stats.Count = 0

	if function != nil {
		frameLoad := stats.frameCount / function.frameCount * 100

		stats.OverFunction.FrameCount = stats.frameCount
		stats.OverFunction.Frame = frameLoad

		stats.OverFunction.AverageCount = stats.avgCount
		stats.OverFunction.Average = stats.avgCount / function.avgCount * 100

		stats.OverFunction.FrameValid = stats.frameCount > 0 && function.frameCount > 0

		if stats.OverFunction.FrameValid {
			if stats.frameCount >= stats.OverFunction.MaxCount {
				stats.OverFunction.MaxCount = stats.frameCount
			}
			if frameLoad >= stats.OverFunction.Max {
				stats.OverFunction.Max = frameLoad
			}
		}

		stats.OverFunction.AverageValid = stats.avgCount > 0 && function.avgCount > 0
		stats.OverFunction.MaxValid = stats.OverFunction.MaxValid || stats.OverFunction.FrameValid
	}

	if src != nil {
		frameLoad := stats.frameCount / src.frameCount * 100

		stats.OverSource.FrameCount = stats.frameCount
		stats.OverSource.Frame = frameLoad

		stats.OverSource.AverageCount = stats.avgCount
		stats.OverSource.Average = stats.avgCount / src.avgCount * 100

		stats.OverSource.FrameValid = stats.frameCount > 0 && src.frameCount > 0

		if stats.OverSource.FrameValid {
			if stats.frameCount >= stats.OverSource.MaxCount {
				stats.OverSource.MaxCount = stats.frameCount
			}
			if frameLoad >= stats.OverSource.Max {
				stats.OverSource.Max = frameLoad
			}
		}

		stats.OverSource.AverageValid = stats.avgCount > 0 && src.avgCount > 0
		stats.OverSource.MaxValid = stats.OverSource.MaxValid || stats.OverSource.FrameValid
	}
}

// Reset the statisics
func (stats *Stats) Reset() {
	stats.OverSource.reset()
	stats.OverFunction.reset()
	stats.allFrameCount = 0.0
	stats.numFrames = 0.0
	stats.avgCount = 0.0
	stats.frameCount = 0.0
	stats.Count = 0.0
	stats.numFrames = 0
}

// KernelVCS indicates the 2600 kernel that is associated with a source function
// or source line.
type KernelVCS int

// List of KernelVCS values.
const (
	KernelAny      KernelVCS = 0x00
	KernelScreen   KernelVCS = 0x01
	KernelVBLANK   KernelVCS = 0x02
	KernelOverscan KernelVCS = 0x04

	// code that is run while the television is in an unstable state
	KernelUnstable KernelVCS = 0x08
)

func (k KernelVCS) String() string {
	switch k {
	case KernelScreen:
		return "Screen"
	case KernelVBLANK:
		return "VBLANK"
	case KernelOverscan:
		return "Overscan"
	case KernelUnstable:
		return "ROM Setup"
	}

	return "Any"
}

// List of KernelVCS values as strings
var AvailableInKernelOptions = []string{"Any", "VBLANK", "Screen", "Overscan", "ROM Setup"}