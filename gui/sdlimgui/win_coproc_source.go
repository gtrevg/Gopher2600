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

package sdlimgui

import (
	"fmt"

	"github.com/inkyblackness/imgui-go/v4"
	"github.com/jetsetilly/gopher2600/coprocessor/developer"
	"github.com/jetsetilly/gopher2600/gui/fonts"
)

// in this case of the coprocessor disassmebly window the actual window title
// is prepended with the actual coprocessor ID (eg. ARM7TDMI). The ID constant
// below is used in the normal way however.

const winCoProcSourceID = "Coprocessor Source"
const winCoProcSourceMenu = "Source"

type winCoProcSource struct {
	img           *SdlImgui
	open          bool
	showAsm       bool
	showNumbering bool
	optionsHeight float32
}

func newWinCoProcSource(img *SdlImgui) (window, error) {
	win := &winCoProcSource{
		img:           img,
		showNumbering: true,
	}
	return win, nil
}

func (win *winCoProcSource) init() {
}

func (win *winCoProcSource) id() string {
	return winCoProcSourceID
}

func (win *winCoProcSource) isOpen() bool {
	return win.open
}

func (win *winCoProcSource) setOpen(open bool) {
	win.open = open
}

func (win *winCoProcSource) draw() {
	if !win.open {
		return
	}

	if !win.img.lz.Cart.HasCoProcBus || win.img.dbg.CoProcDev == nil {
		return
	}

	imgui.SetNextWindowPosV(imgui.Vec2{465, 285}, imgui.ConditionFirstUseEver, imgui.Vec2{0, 0})
	imgui.SetNextWindowSizeV(imgui.Vec2{551, 526}, imgui.ConditionFirstUseEver)
	imgui.SetNextWindowSizeConstraints(imgui.Vec2{551, 300}, imgui.Vec2{800, 1000})

	title := fmt.Sprintf("%s %s", win.img.lz.Cart.CoProcID, winCoProcSourceID)
	imgui.BeginV(title, &win.open, imgui.WindowFlagsNone)
	defer imgui.End()

	if win.img.dbg.CoProcDev == nil {
		imgui.Text("No source files available")
		return
	}

	// new child that contains the main scrollable table
	imgui.BeginChildV("##coprocSourceMain", imgui.Vec2{X: 0, Y: imguiRemainingWinHeight() - win.optionsHeight}, false, 0)
	imgui.BeginTabBar("##coprocSourceTabBar")

	// safely iterate over source codet
	win.img.dbg.CoProcDev.BorrowSource(func(src *developer.Source) {
		for _, fn := range src.FilesNames {
			if imgui.BeginTabItemV(fn, nil, 0) {
				imgui.BeginChildV("lastexecution", imgui.Vec2{X: 0, Y: imguiRemainingWinHeight()}, false, 0)
				imgui.BeginTableV("##coprocSourceTable", 5, imgui.TableFlagsSizingFixedFit, imgui.Vec2{}, 0.0)

				// first column is a dummy column so that Selectable (span all columns) works correctly
				imgui.TableSetupColumnV("", imgui.TableColumnFlagsNone, 1, 0)
				imgui.TableSetupColumnV("", imgui.TableColumnFlagsNone, 20, 1)
				imgui.TableSetupColumnV("", imgui.TableColumnFlagsNone, 30, 2)
				imgui.TableSetupColumnV("", imgui.TableColumnFlagsNone, 40, 3)

				var clipper imgui.ListClipper
				clipper.Begin(len(src.Files[fn].Lines))
				for clipper.Step() {
					for i := clipper.DisplayStart; i < clipper.DisplayEnd; i++ {
						ln := src.Files[fn].Lines[i]
						imgui.TableNextRow()

						// highlight line mouse is over
						imgui.TableNextColumn()
						imgui.PushStyleColor(imgui.StyleColorHeaderHovered, win.img.cols.DisasmHover)
						imgui.PushStyleColor(imgui.StyleColorHeaderActive, win.img.cols.DisasmHover)
						imgui.SelectableV("", false, imgui.SelectableFlagsSpanAllColumns, imgui.Vec2{0, 0})
						imgui.PopStyleColorV(2)

						// show chip icon and also tooltip if mouse is hovered
						// on selectable
						imgui.TableNextColumn()
						if len(ln.Asm) > 0 {
							if win.showAsm {
								imguiTooltip(func() {
									limit := 0
									for _, asm := range ln.Asm {
										imgui.Text(asm.Asm)
										limit++
										if limit > 10 {
											imgui.Text("...more")
											break // for loop
										}
									}
								}, true)
							}

							imgui.Text(string(fonts.Chip))
						}

						// percentage of time taken by this line
						imgui.TableNextColumn()
						if ln.CycleCount > 0 {
							imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.DisasmOperator)
							imgui.Text(fmt.Sprintf("%0.1f%%", ln.CycleCount/src.TotalCycleCount*100.0))
							imgui.PopStyleColor()
						}

						// line numbering
						imgui.TableNextColumn()
						if win.showNumbering {
							imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.DisasmAddress)
							imgui.Text(fmt.Sprintf("%d", ln.LineNumber))
							imgui.PopStyleColor()
						}

						// source line
						imgui.TableNextColumn()
						imgui.Text(ln.Content)
					}
				}

				imgui.EndTable()
				imgui.EndChild()
				imgui.EndTabItem()
			}
		}
	})

	imgui.EndTabBar()
	imgui.EndChild()

	// options toolbar at foot of window
	win.optionsHeight = imguiMeasureHeight(func() {
		imgui.Separator()
		imgui.Spacing()
		imgui.Checkbox("Show ASM in Tooltip", &win.showAsm)
		imgui.SameLineV(0, 15)
		imgui.Checkbox("Show Numbering", &win.showNumbering)
	})
}
