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
	"strconv"
	"strings"

	"github.com/inkyblackness/imgui-go/v4"
	"github.com/jetsetilly/gopher2600/gui/sdlimgui/fonts"
)

// return the height of the window from the current cursor position to the end
// of the window frame. useful for calculating scroll areas for windows with a
// static header.
//
// the height of a static footer must be subtracted from the returned value.
// the measuredHeight() function is useful for measuring footers.
func imguiRemainingWinHeight() float32 {
	return imgui.WindowHeight() - imgui.CursorPosY() - imgui.CurrentStyle().FramePadding().Y*2 - imgui.CurrentStyle().ItemInnerSpacing().Y
}

// return the width of the window from the current cursor position to the edge
// of the window frame. subtracts padding and spacing from the edge to make it
// suitable for sizing buttons etc.
func imguiRemainingWinWidth() float32 {
	w := imgui.WindowWidth() - imgui.CursorPosX()
	w -= imgui.CurrentStyle().FramePadding().X + imgui.CurrentStyle().ItemInnerSpacing().X
	return w
}

// divide remaining win width by n taking into account spacing between
// widgets. useful for cheap tabulation of buttons.
func imguiDivideWinWidth(n int) float32 {
	w := imguiRemainingWinWidth() / float32(n)
	w -= imgui.CurrentStyle().FramePadding().X
	return w
}

// returns the minimum Vec2{} required to fit any of the string values listed
// in the arguments.
func imguiGetFrameDim(s string, t ...string) imgui.Vec2 {
	w := imgui.CalcTextSize(s, false, 0)
	for i := range t {
		y := imgui.CalcTextSize(t[i], false, 0)
		if y.X > w.X {
			w = y
		}
	}

	w.Y = imgui.FontSize() + (imgui.CurrentStyle().FramePadding().Y * 2.5)

	// comboboxes in particuar look better with a small amount of trailing space
	w.X += imgui.CurrentStyle().FramePadding().X * 2.5

	return w
}

// returns the pixel width of a text string length characters wide. assumes all
// characters are of the same width. Uses the 'X' character for measurement.
func imguiTextWidth(length int) float32 {
	return imguiGetFrameDim(strings.Repeat("X", length)).X
}

// return coordinates for right alignment of a string to previous imgui widget.
// func imguiRightAlign(s string) imgui.Vec2 {
// 	// this dearimgui dance gets the X position of the end of the last widget.
// 	// leaving us with c, a Vec2 with the correct Y position
// 	c := imgui.CursorPos()
// 	imgui.SameLine()
// 	x := imgui.CursorPosX()
// 	imgui.SetCursorPos(c)
// 	c = imgui.CursorPos()

// 	// the X coordinate can be set by subtracting the width of the text from
// 	// the stored x value
// 	c.X = x - imguiTextWidth(len(s)) + imgui.CurrentStyle().FramePadding().X

// 	return c
// }

// adds min/max indicators to imgui.SliderInt. returns true if slider has changed.
// func imguiSliderInt(label string, f *int32, s int32, e int32) bool {
// 	v := imgui.SliderInt(label, f, s, e)

// 	// alignment information for frame number indicators below
// 	min := fmt.Sprintf("%d", s)
// 	max := fmt.Sprintf("%d", e)
// 	align := imguiRightAlign(max)

// 	// rewind frame information
// 	imgui.Text(min)
// 	imgui.SameLine()
// 	imgui.SetCursorPos(align)
// 	imgui.Text(max)

// 	return v
// }

// draw toggle button at current cursor position. returns true if toggle has been clicked.
func imguiToggleButton(id string, v bool, col imgui.Vec4) bool {
	height := imgui.FrameHeight() * 0.75
	width := height * 1.55
	radius := height * 0.50
	t := float32(0.0)
	if v {
		t = 1.0
	}

	bg := imgui.PackedColorFromVec4(col)
	p := imgui.CursorScreenPos().Plus(imgui.Vec2{X: 0, Y: (imgui.FrameHeight() / 2) - (height / 2)})
	dl := imgui.WindowDrawList()

	r := false

	imgui.InvisibleButtonV(id, imgui.Vec2{width, height}, imgui.ButtonFlagsMouseButtonLeft)
	if imgui.IsItemClicked() {
		r = true
	}

	dl.AddRectFilledV(p, imgui.Vec2{p.X + width, p.Y + height}, bg, radius, imgui.DrawCornerFlagsAll)
	dl.AddCircleFilled(imgui.Vec2{p.X + radius + t*(width-radius*2.0), p.Y + radius},
		radius-1.5, imgui.PackedColorFromVec4(imgui.Vec4{1.0, 1.0, 1.0, 1.0}))

	return r
}

// draw vertical toggle button at current cursor position. returns true if toggle has been clicked.
//
// NOTE: this function has been hacked to work with the status register toggles
// in win_cpu. any changes to this function will have to bear that in mind.
func imguiToggleButtonVertical(id string, v bool, col imgui.Vec4) bool {
	bg := imgui.PackedColorFromVec4(col)
	p := imgui.CursorScreenPos().Minus(imgui.Vec2{X: 0, Y: 1})
	dl := imgui.WindowDrawList()

	width := imgui.CalcTextSize(" X", false, 0.0).X
	height := width * 1.55
	radius := width * 0.50
	t := float32(0.0)
	if v {
		t = 1.0
	}

	r := false

	imgui.InvisibleButtonV(id, imgui.Vec2{width, height}, imgui.ButtonFlagsMouseButtonLeft)
	if imgui.IsItemClicked() {
		r = true
	}

	dl.AddRectFilledV(p, imgui.Vec2{p.X + width, p.Y + height}, bg, radius, imgui.DrawCornerFlagsAll)
	dl.AddCircleFilled(imgui.Vec2{p.X + radius, p.Y + radius + t*(width*0.5)},
		radius-1.5, imgui.PackedColorFromVec4(imgui.Vec4{1.0, 1.0, 1.0, 1.0}))

	return r
}

// imguiBooleanButton with dimension argument.
func imguiBooleanButton(cols *imguiColors, state bool, text string, dim imgui.Vec2) bool {
	if state {
		imgui.PushStyleColor(imgui.StyleColorButton, cols.True)
		imgui.PushStyleColor(imgui.StyleColorButtonHovered, cols.True)
		imgui.PushStyleColor(imgui.StyleColorButtonActive, cols.True)
	} else {
		imgui.PushStyleColor(imgui.StyleColorButton, cols.False)
		imgui.PushStyleColor(imgui.StyleColorButtonHovered, cols.False)
		imgui.PushStyleColor(imgui.StyleColorButtonActive, cols.False)
	}
	b := imgui.ButtonV(text, dim)
	imgui.PopStyleColorV(3)
	return b
}

// imguiLabel aligns text with widget borders and positions cursor so next
// widget will follow the label. where a label parameter is required by a
// widget and you do not want it to appear, preferring the label given by
// imguiLabel(), you can use the empty string or use the double hash construct.
// For example
//
//		imgui.SliderInt("##foo", &v, s, e)
//		imguiLabel("My Slider")
func imguiLabel(text string) {
	imgui.AlignTextToFramePadding()
	imgui.Text(text)
	imgui.SameLine()
}

// imguiLabelEnd is the same imguiLabel but without the instruction to put the
// next widget on the same line.
func imguiLabelEnd(text string) {
	imgui.AlignTextToFramePadding()
	imgui.Text(text)
}

// position cursor for indented imgui.Text().
func imguiIndentText(text string) {
	p := imgui.CursorPos()
	p.X += 10
	imgui.SetCursorPos(p)
	imgui.Text(text)
}

// imguiMeasureHeight returns the height of the region drawn in the region() function.
func imguiMeasureHeight(region func()) float32 {
	p := imgui.CursorPos()
	region()
	return imgui.CursorPos().Minus(p).Y
}

// imguiMeasureWidth returns the width of the region drawn in the region()
// function.
//
// it's a bit tricky getting the width with dear imgui. it involves noting the
// current position, calling SameLine(), performing the width measurement and
// returning to the stored position.
//
// this seems to work but it caused odd results when used to measure the width
// of a table.
// func imguiMeasureWidth(region func()) float32 {
// 	p := imgui.CursorPos()
// 	region()
// 	defer imgui.SetCursorPos(imgui.CursorPos())
// 	imgui.SameLine()
// 	return imgui.CursorPos().Minus(p).X
// }

// pads imgui.Separator with additional spacing.
func imguiSeparator() {
	imgui.Spacing()
	imgui.Separator()
	imgui.Spacing()
}

// draw grid of bytes. useful for memory representation (RAM, etc.)
func drawByteGrid(data []uint8, cmp []uint8, diffCol imgui.Vec4, base uint16, commit func(uint16, uint8)) {
	// format string for column headers. the number of digits required depends
	// on the length of the data slice
	columnFormat := fmt.Sprintf("%%0%dx- ", len(fmt.Sprintf("%x", len(data)-1))-1)

	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, imgui.Vec2{})
	imgui.PushItemWidth(imguiTextWidth(2))

	defer imgui.PopStyleVar()
	defer imgui.PopItemWidth()

	const gridWidth = 16

	// draw headers for each column
	headerDim := imgui.Vec2{X: imguiTextWidth(4), Y: imgui.CursorPosY()}
	for i := 0; i < gridWidth; i++ {
		imgui.SetCursorPos(headerDim)
		headerDim.X += imguiTextWidth(2)
		imgui.AlignTextToFramePadding()
		imgui.Text(fmt.Sprintf("-%x", i))
	}

	var clipper imgui.ListClipper
	clipper.Begin(len(data) / gridWidth)

	for clipper.Step() {
		for i := clipper.DisplayStart; i < clipper.DisplayEnd; i++ {
			for j := 0; j < gridWidth; j++ {
				offset := uint16(i*gridWidth) + uint16(j)
				addr := base + offset

				// draw column header
				if j == 0 {
					imgui.AlignTextToFramePadding()
					imgui.Text(fmt.Sprintf(columnFormat, addr/16))
					imgui.SameLine()
				} else {
					imgui.SameLine()
				}

				// editable byte
				b := data[offset]

				// compare current RAM value with value in comparison snapshot and use
				// highlight color if it is different
				c := b
				if cmp != nil {
					c = cmp[offset]
				}
				if b != c {
					imgui.PushStyleColor(imgui.StyleColorFrameBg, diffCol)
				}

				s := fmt.Sprintf("%02x", b)
				if imguiHexInput(fmt.Sprintf("##%d", addr), 2, &s) {
					if v, err := strconv.ParseUint(s, 16, 8); err == nil {
						commit(addr, uint8(v))
					}
				}

				if b != c {
					imguiTooltipSimple(fmt.Sprintf("was %02x -> is now %02x", c, b))
				}

				// undo any color changes
				if b != c {
					imgui.PopStyleColor()
				}
			}
		}
	}
}

// imguiColorLabel displays a coloured icon (fonts.ColorSwatch) with a label.
// useful for generating color keys.
func imguiColorLabel(label string, col imgui.Vec4) {
	imgui.BeginGroup()
	imgui.PushStyleColor(imgui.StyleColorText, col)
	imgui.Text(string(fonts.ColorSwatch))
	imgui.PopStyleColor()
	imgui.SameLine()
	imgui.Text(label)
	imgui.EndGroup()
}

// returns a Vec2 suitable for use as a position vector when opening a imgui
// window. The X and Y are set such that 0.0 <= value <= 1.0
//
// the vector is weighted to reflect the quadrant in which the supplied
// argument p (mouse position, say) falls within.
func (img *SdlImgui) imguiWindowQuadrant(p imgui.Vec2) imgui.Vec2 {
	sp := img.wm.screenPos
	ww, wh := img.plt.window.GetSize()

	q := imgui.Vec2{X: 0.25, Y: 0.25}

	if p.X > (sp.X+float32(ww))/2 {
		q.X = 0.75
	}

	if p.Y > (sp.Y+float32(wh))/2 {
		q.Y = 0.75
	}

	return q
}

// use appropriate palette for television spec.
func (img *SdlImgui) imguiTVPalette() (string, packedPalette, []imgui.Vec4) {
	switch img.lz.TV.Spec.ID {
	case "PAL":
		return "PAL", img.cols.packedPalettePAL, img.cols.paletteNTSC
	case "PAL60":
		return "PAL60", img.cols.packedPalettePAL, img.cols.palettePAL
	case "NTSC":
		return "NTSC", img.cols.packedPaletteNTSC, img.cols.paletteNTSC
	}

	return "unknown", img.cols.packedPaletteNTSC, img.cols.paletteNTSC
}

// draw swatch. returns true if clicked. a good response to a click event is to
// open up an instance of popupPalette.
//
// size argument should be expressed as a fraction the fraction will be applied
// to imgui.FontSize() to obtain the radius of the swatch.
func (img *SdlImgui) imguiSwatch(col uint8, size float32) (clicked bool) {
	_, pal, _ := img.imguiTVPalette()
	c := pal[col]

	r := imgui.FontSize() * size

	// position & dimensions of swatch
	l := imgui.FontSize() * 0.75
	p := imgui.CursorScreenPos()
	p.X += r
	p.Y += l

	// if mouse is clicked in the range of the swatch. very simple detection,
	// not accounting for the fact that the swatch is visibly circular
	if imgui.IsWindowHovered() && imgui.IsMouseClicked(0) {
		pos := imgui.MousePos()
		clicked = pos.X >= p.X-r && pos.X <= p.X+r && pos.Y >= p.Y-r && pos.Y <= p.Y+r
	}

	// draw swatch
	dl := imgui.WindowDrawList()
	dl.AddCircleFilled(p, r, c)

	// set up cursor for next widget
	p.X += 2 * r
	p.Y -= l
	imgui.SetCursorScreenPos(p)

	return clicked
}

// shows tooltip on hover of the previous imgui digest/group. useful for simple
// tooltips.
func imguiTooltipSimple(tooltip string) {
	// split string on newline and display with seperate calls to imgui.Text()
	tooltip = strings.TrimSpace(tooltip)
	if tooltip != "" && imgui.IsItemHovered() {
		s := strings.Split(tooltip, "\n")
		imgui.BeginTooltip()
		for _, t := range s {
			imgui.Text(t)
		}
		imgui.EndTooltip()
	}
}

// shows tooltip that require more complex formatting than a single string.
//
// the hoverTest argument says that the tooltip should be displayed only
// when the last imgui widget/group is being hovered over with the mouse. if
// this argument is false then it implies that the decision to show the tooltip
// has already been made by the calling function.
func imguiTooltip(f func(), hoverTest bool) {
	if !hoverTest || imgui.IsItemHovered() {
		imgui.BeginTooltip()
		f()
		imgui.EndTooltip()
	}
}
