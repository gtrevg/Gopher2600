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
	"os"
	"strings"

	"github.com/inkyblackness/imgui-go/v4"
	"github.com/jetsetilly/gopher2600/coprocessor/developer"
	"github.com/jetsetilly/gopher2600/gui/fonts"
	"github.com/jetsetilly/gopher2600/logger"
	"github.com/jetsetilly/gopher2600/resources/unique"
)

// in this case of the coprocessor disassmebly window the actual window title
// is prepended with the actual coprocessor ID (eg. ARM7TDMI). The ID constant
// below is used in the normal way however.

const winCoProcGlobalsID = "Coprocessor Global Variables"
const winCoProcGlobalsMenu = "Globals"

type winCoProcGlobals struct {
	debuggerWin

	img *SdlImgui

	firstOpen bool

	selectedFile          *developer.SourceFile
	selectedFileComboOpen bool

	optionsHeight  float32
	showAllGlobals bool

	openNodes map[string]bool
}

func newWinCoProcGlobals(img *SdlImgui) (window, error) {
	win := &winCoProcGlobals{
		img:       img,
		firstOpen: true,
		openNodes: make(map[string]bool),
	}
	return win, nil
}

func (win *winCoProcGlobals) init() {
}

func (win *winCoProcGlobals) id() string {
	return winCoProcGlobalsID
}

func (win *winCoProcGlobals) debuggerDraw() {
	if !win.debuggerOpen {
		return
	}

	if !win.img.lz.Cart.HasCoProcBus || win.img.dbg.CoProcDev == nil {
		return
	}

	imgui.SetNextWindowPosV(imgui.Vec2{982, 77}, imgui.ConditionFirstUseEver, imgui.Vec2{0, 0})
	imgui.SetNextWindowSizeV(imgui.Vec2{520, 390}, imgui.ConditionFirstUseEver)
	imgui.SetNextWindowSizeConstraints(imgui.Vec2{400, 300}, imgui.Vec2{700, 1000})

	title := fmt.Sprintf("%s %s", win.img.lz.Cart.CoProcID, winCoProcGlobalsID)
	if imgui.BeginV(win.debuggerID(title), &win.debuggerOpen, imgui.WindowFlagsNone) {
		win.draw()
	}

	win.debuggerGeom.update()
	imgui.End()
}

func (win *winCoProcGlobals) draw() {
	win.img.dbg.CoProcDev.BorrowSource(func(src *developer.Source) {
		if src == nil {
			imgui.Text("No source files available")
			return
		}

		if len(src.Filenames) == 0 {
			imgui.Text("No source files available")
			return
		}

		if src.SortedGlobals.Len() == 0 {
			imgui.Text("No global variable in the source")
			return
		}

		if win.firstOpen {
			// assume source entry point is a function called "main"
			if m, ok := src.Functions["main"]; ok {
				win.selectedFile = m.DeclLine.File
			} else {
				// if main does not exists then open at the first file in the list
				for _, fn := range src.Filenames {
					if src.Files[fn].HasGlobals {
						win.selectedFile = src.Files[fn]
						break // for loop
					}
				}
			}

			win.firstOpen = false
		}

		if !win.showAllGlobals {
			imgui.AlignTextToFramePadding()
			imgui.Text("Filename")
			imgui.SameLine()
			imgui.PushItemWidth(imgui.ContentRegionAvail().X)
			if imgui.BeginComboV("##selectedFile", win.selectedFile.ShortFilename, imgui.ComboFlagsHeightRegular) {
				for _, fn := range src.Filenames {
					// skip files that have no global variables
					if !src.Files[fn].HasGlobals {
						continue
					}

					if imgui.Selectable(src.Files[fn].ShortFilename) {
						win.selectedFile = src.Files[fn]
					}

					// set scroll on the first frame that the combo is open
					if !win.selectedFileComboOpen && fn == win.selectedFile.Filename {
						imgui.SetScrollHereY(0.0)
					}
				}

				imgui.EndCombo()

				// note that combo is open *after* it has been drawn
				win.selectedFileComboOpen = true
			} else {
				win.selectedFileComboOpen = false
			}
			imgui.PopItemWidth()

			imgui.Spacing()
		}

		// global variable table for the selected file

		const numColumns = 4

		flgs := imgui.TableFlagsScrollY
		flgs |= imgui.TableFlagsSizingStretchProp
		flgs |= imgui.TableFlagsSortable
		flgs |= imgui.TableFlagsNoHostExtendX
		flgs |= imgui.TableFlagsResizable

		imgui.BeginTableV("##globalsTable", numColumns, flgs, imgui.Vec2{Y: imguiRemainingWinHeight() - win.optionsHeight}, 0.0)

		// setup columns. the labelling column 2 depends on whether the coprocessor
		// development instance has source available to it
		width := imgui.ContentRegionAvail().X
		imgui.TableSetupColumnV("Name", imgui.TableColumnFlagsPreferSortDescending|imgui.TableColumnFlagsDefaultSort, width*0.40, 0)
		imgui.TableSetupColumnV("Type", imgui.TableColumnFlagsNoSort, width*0.20, 1)
		imgui.TableSetupColumnV("Address", imgui.TableColumnFlagsPreferSortDescending, width*0.15, 2)
		imgui.TableSetupColumnV("Value", imgui.TableColumnFlagsNoSort, width*0.20, 3)

		imgui.TableSetupScrollFreeze(0, 1)
		imgui.TableHeadersRow()

		for i, varb := range src.SortedGlobals.Variables {
			if win.showAllGlobals || varb.DeclLine.File.Filename == win.selectedFile.Filename {
				win.drawVariable(src, varb, 0, 0, false, fmt.Sprint(i))
			}
		}

		sort := imgui.TableGetSortSpecs()
		if sort.SpecsDirty() {
			for _, s := range sort.Specs() {
				switch s.ColumnUserID {
				case 0:
					src.SortedGlobals.SortByName(s.SortDirection == imgui.SortDirectionAscending)
				case 2:
					src.SortedGlobals.SortByAddress(s.SortDirection == imgui.SortDirectionAscending)
				}
			}
			sort.ClearSpecsDirty()
		}

		imgui.EndTable()

		win.optionsHeight = imguiMeasureHeight(func() {
			imgui.Spacing()
			imgui.Separator()
			imgui.Spacing()
			imgui.Checkbox("List all globals (in all files)", &win.showAllGlobals)
			imgui.SameLineV(0, 20)
			if imgui.Button(fmt.Sprintf("%c Save to CSV", fonts.Disk)) {
				win.saveToCSV(src)
			}
		})
	})
}

func (win *winCoProcGlobals) drawVariableTooltip(varb *developer.SourceVariable, value uint32) {
	imguiTooltip(func() {
		drawVariableTooltip(varb, value, win.img.cols)
	}, true)
}

func drawVariableTooltip(varb *developer.SourceVariable, value uint32, cols *imguiColors) {
	imgui.PushStyleColor(imgui.StyleColorText, cols.CoProcVariablesAddress)
	imgui.Text(fmt.Sprintf("%08x", varb.Address))
	imgui.PopStyleColor()

	imgui.Text(varb.Name)
	imgui.SameLine()
	imgui.PushStyleColor(imgui.StyleColorText, cols.CoProcVariablesType)
	imgui.Text(varb.Type.Name)
	imgui.PopStyleColor()

	imgui.PushStyleColor(imgui.StyleColorText, cols.CoProcVariablesTypeSize)
	imgui.Text(fmt.Sprintf("%d bytes", varb.Type.Size))
	imgui.PopStyleColor()

	if varb.IsArray() {
		imgui.Spacing()
		imgui.Separator()
		imgui.Spacing()
		imgui.Text(fmt.Sprintf("is an array of %d elements", varb.Type.ElementCount))
	} else if varb.IsComposite() {
		imgui.Spacing()
		imgui.Separator()
		imgui.Spacing()
		imgui.Text(fmt.Sprintf("is a struct of %d members", len(varb.Type.Members)))
	} else {
		imgui.Spacing()
		imgui.Separator()
		imgui.Spacing()

		if imgui.BeginTableV("##variablevalues", 2, imgui.TableFlagsNone, imgui.Vec2{}, 0.0) {
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Dec: ")
			imgui.TableNextColumn()
			imgui.PushStyleColor(imgui.StyleColorText, cols.CoProcVariablesNotes)
			imgui.Text(fmt.Sprintf("%d", value))
			imgui.PopStyleColor()

			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Hex: ")
			imgui.TableNextColumn()
			imgui.PushStyleColor(imgui.StyleColorText, cols.CoProcVariablesNotes)
			hex := fmt.Sprintf(varb.Type.Hex(), value)
			imgui.Text(hex[:2])

			for i := 1; i < len(hex)/2; i++ {
				imgui.SameLine()
				s := i * 2
				imgui.Text(fmt.Sprintf("%s", hex[s:s+2]))
			}

			imgui.PopStyleColor()

			// binary information is a little more complex to draw. we split
			// the binary value into bytes and display vertically
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Bin: ")

			imgui.TableNextColumn()
			imgui.PushStyleColor(imgui.StyleColorText, cols.CoProcVariablesNotes)
			bin := fmt.Sprintf(varb.Type.Bin(), value)
			imgui.Text(bin[:8])

			for i := 1; i < len(bin)/8; i++ {
				s := i * 8
				imgui.Text(bin[s : s+8])
			}

			imgui.PopStyleColor()
		}

		imgui.EndTable()
	}

	imgui.Spacing()
	imgui.Separator()
	imgui.Spacing()

	imgui.Text(varb.DeclLine.File.ShortFilename)
	imgui.PushStyleColor(imgui.StyleColorText, cols.CoProcSourceLineNumber)
	imgui.Text(fmt.Sprintf("Line: %d", varb.DeclLine.LineNumber))
	imgui.PopStyleColor()
}

func (win *winCoProcGlobals) drawVariable(src *developer.Source,
	varb *developer.SourceVariable, baseAddress uint64,
	indentLevel int, unnamed bool, nodeID string) {

	address := varb.Address
	if varb.AddressIsOffset() {
		// address of variable is an offset of parent address
		address += baseAddress
	}

	const IndentDepth = 2

	var name string
	if unnamed {
		name = fmt.Sprintf("%s%s", strings.Repeat(" ", IndentDepth*indentLevel), string(fonts.Pointer))
	} else {
		name = fmt.Sprintf("%s%s", strings.Repeat(" ", IndentDepth*indentLevel), varb.Name)
	}

	imgui.TableNextRow()

	imgui.TableNextColumn()
	imgui.PushStyleColor(imgui.StyleColorHeaderHovered, win.img.cols.CoProcSourceHover)
	imgui.PushStyleColor(imgui.StyleColorHeaderActive, win.img.cols.CoProcSourceHover)
	imgui.SelectableV(name, false, imgui.SelectableFlagsSpanAllColumns, imgui.Vec2{0, 0})
	imgui.PopStyleColorV(2)

	if varb.IsComposite() || varb.IsArray() {
		win.drawVariableTooltip(varb, 0)

		if imgui.IsItemClicked() {
			win.openNodes[nodeID] = !win.openNodes[nodeID]
		}

		imgui.TableNextColumn()
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.CoProcVariablesType)
		imgui.Text(varb.Type.Name)
		imgui.PopStyleColor()

		imgui.TableNextColumn()
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.CoProcVariablesAddress)
		imgui.Text(fmt.Sprintf("%08x", address))
		imgui.PopStyleColor()

		imgui.TableNextColumn()
		if win.openNodes[nodeID] {
			imgui.Text(string(fonts.TreeOpen))
		} else {
			imgui.Text(string(fonts.TreeClosed))
		}

		if win.openNodes[nodeID] {
			if varb.IsComposite() {
				for i, memb := range varb.Type.Members {
					win.drawVariable(src, memb, address, indentLevel+1, false, fmt.Sprint(nodeID, i))
				}
			} else if varb.IsArray() {
				for i := 0; i < varb.Type.ElementCount; i++ {
					elem := &developer.SourceVariable{
						Name:     fmt.Sprintf("%s[%d]", varb.Name, i),
						Type:     varb.Type.ElementType,
						DeclLine: varb.DeclLine,
						Address:  address + uint64(i*varb.Type.ElementType.Size),
					}
					win.drawVariable(src, elem, elem.Address, indentLevel+1, false, fmt.Sprint(nodeID, i))
				}
			}
		}
	} else if varb.IsPointer() {
		if imgui.IsItemClicked() {
			win.openNodes[nodeID] = !win.openNodes[nodeID]
		}

		value, valueOk := win.readMemory(address)
		value &= varb.Type.Mask()

		if valueOk {
			win.drawVariableTooltip(varb, value)
		}

		imgui.TableNextColumn()
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.CoProcVariablesType)
		imgui.Text(varb.Type.Name)
		imgui.PopStyleColor()

		imgui.TableNextColumn()
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.CoProcVariablesAddress)
		imgui.Text(fmt.Sprintf("%08x", address))
		imgui.PopStyleColor()

		imgui.TableNextColumn()

		dereference := false
		if win.openNodes[nodeID] {
			imgui.Text(string(fonts.TreeOpen))
			imgui.SameLine()
			dereference = true
		} else {
			imgui.Text(string(fonts.TreeClosed))
			imgui.SameLine()
		}

		if valueOk {
			imgui.Text(fmt.Sprintf("*%s", fmt.Sprintf(varb.Type.Hex(), value)))
		} else {
			imgui.Text("-")
		}

		if dereference {
			deref := &developer.SourceVariable{
				Name:     varb.Name,
				Type:     varb.Type.PointerType,
				DeclLine: varb.DeclLine,
				Address:  uint64(value),
			}
			win.drawVariable(src, deref, deref.Address, indentLevel+1, true, fmt.Sprint(nodeID, 1))
		}
	} else {
		value, valueOk := win.readMemory(address)
		value &= varb.Type.Mask()

		if valueOk {
			win.drawVariableTooltip(varb, value)
		}

		imgui.TableNextColumn()
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.CoProcVariablesType)
		imgui.Text(varb.Type.Name)
		imgui.PopStyleColor()

		imgui.TableNextColumn()
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.CoProcVariablesAddress)
		imgui.Text(fmt.Sprintf("%08x", address))
		imgui.PopStyleColor()

		imgui.TableNextColumn()
		if valueOk {
			imgui.Text(fmt.Sprintf(varb.Type.Hex(), value))
		} else {
			imgui.Text("-")
		}
	}
}

func (win *winCoProcGlobals) readMemory(address uint64) (uint32, bool) {
	if !win.img.lz.Cart.HasStaticBus {
		return 0, false
	}
	return win.img.lz.Cart.Static.Read32bit(uint32(address))
}

// save all variables in the curent view to a CSV file in the working
// directory. filename will be of the form:
//
// globals_<cart name>_<timestamp>.csv
//
// all entries in the current view are saved, including closed nodes.
func (win *winCoProcGlobals) saveToCSV(src *developer.Source) {

	// open unique file
	fn := unique.Filename("globals", win.img.lz.Cart.Shortname)
	fn = fmt.Sprintf("%s.csv", fn)
	f, err := os.Create(fn)
	if err != nil {
		logger.Logf("sdlimgui", "could not save globals CSV: %v", err)
		return
	}
	defer func() {
		err := f.Close()
		if err != nil {
			logger.Logf("sdlimgui", "error saving globals CSV: %v", err)
		}
	}()

	// name of parent
	parentName := func(parent, name string) string {
		if parent == "" {
			return name
		}
		return fmt.Sprintf("%s->%s", parent, name)
	}

	// write string to CSV file
	writeEntry := func(s string) {
		f.WriteString(s)
		f.WriteString("\n")
	}

	// the builEntry function is recursive and will is very similar in
	// structure to the drawVariable() function above
	var buildEntry func(*developer.SourceVariable, uint64, string)
	buildEntry = func(varb *developer.SourceVariable, baseAddress uint64, parent string) {
		if win.showAllGlobals || varb.DeclLine.File.Filename == win.selectedFile.Filename {
			s := strings.Builder{}

			address := varb.Address
			if varb.AddressIsOffset() {
				// address of variable is an offset of parent address
				address += baseAddress
			}

			if varb.IsComposite() {
				for _, memb := range varb.Type.Members {
					buildEntry(memb, address, parentName(parent, varb.Name))
				}
			} else if varb.IsArray() {
				for i := 0; i < varb.Type.ElementCount; i++ {
					elem := &developer.SourceVariable{
						Name:     fmt.Sprintf("%s[%d]", varb.Name, i),
						Type:     varb.Type.ElementType,
						DeclLine: varb.DeclLine,
						Address:  address + uint64(i*varb.Type.ElementType.Size),
					}
					buildEntry(elem, elem.Address, parent)
				}
			} else if varb.IsPointer() {
				value, valueOk := win.readMemory(address)
				value &= varb.Type.Mask()

				s.WriteString(fmt.Sprintf("%s,", parent))
				s.WriteString(fmt.Sprintf("%s,", varb.Name))
				s.WriteString(fmt.Sprintf("%s,", varb.Type.Name))
				s.WriteString(fmt.Sprintf("%08x,", address))
				if valueOk {
					s.WriteString("*")
					s.WriteString(fmt.Sprintf(varb.Type.Hex(), value))
					s.WriteString(",")
				} else {
					s.WriteString("-,")
				}
				writeEntry(s.String())

				deref := &developer.SourceVariable{
					Name:     varb.Name,
					Type:     varb.Type.PointerType,
					DeclLine: varb.DeclLine,
					Address:  uint64(value),
				}

				buildEntry(deref, deref.Address, parentName(parent, varb.Name))
			} else {
				value, valueOk := win.readMemory(address)
				value &= varb.Type.Mask()

				s.WriteString(fmt.Sprintf("%s,", parent))
				s.WriteString(fmt.Sprintf("%s,", varb.Name))
				s.WriteString(fmt.Sprintf("%s,", varb.Type.Name))
				s.WriteString(fmt.Sprintf("%08x,", address))

				if valueOk {
					s.WriteString(fmt.Sprintf(varb.Type.Hex(), value))
					s.WriteString(",")
				}

				writeEntry(s.String())
			}
		}
	}

	// write header to CSV file
	writeEntry("Parent, Name, Type, Address, Value")

	// process every variable in the current view
	for _, varb := range src.SortedGlobals.Variables {
		buildEntry(varb, 0, "")
	}
}
