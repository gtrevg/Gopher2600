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

package gui

// FeatureReq is used to request the setting of a gui attribute
// eg. toggling the overlay.
type FeatureReq string

// FeatureReqData represents the information associated with a FeatureReq. See
// commentary for the defined FeatureReq values for the underlying type.
type FeatureReqData interface{}

// List of valid feature requests. argument must be of the type specified or
// else the interface{} type conversion will fail and the application will
// probably crash.
//
// Note that, like the name suggests, these are requests, they may or may not
// be satisfied depending other conditions in the GUI.
const (
	// notify gui of the underlying emulation mode.
	ReqSetEmulationMode FeatureReq = "ReqSetEmulationMode" // emulation.Mode

	// program is ending. gui should do anything required before final exit.
	ReqEnd FeatureReq = "ReqEnd" // nil

	// whether gui should try to sync with the monitor refresh rate. not all
	// gui modes have to obey this but for presentation/play modes it's a good
	// idea to have it set.
	ReqMonitorSync FeatureReq = "ReqMonitorSync" // bool

	// whether the gui is visible or not. results in an error if requested in
	// playmode.
	ReqSetVisibility FeatureReq = "ReqSetVisibility" // bool

	// put gui output into full-screen mode (ie. no window border and content
	// the size of the monitor).
	ReqFullScreen FeatureReq = "ReqFullScreen" // bool

	// special request for PlusROM cartridges.
	ReqPlusROMFirstInstallation FeatureReq = "ReqPlusROMFirstInstallation" // none

	// controller has changed for one of the ports. the string is a description
	// of the controller.
	ReqControllerChange FeatureReq = "ReqControllerChange" // plugging.PortID, plugging.PeripheralID

	// an event generated by the emulation has occured. for example, the
	// emulation has been paused.
	ReqEmulationEvent FeatureReq = "ReqEmulationEvent" // emulation.Event

	// an event generated by the cartridge has occured. for example, network
	// activity from a PlusROM cartridge.
	ReqCartridgeEvent FeatureReq = "ReqCartridgeEvent" // mapper.Event

	// open ROM selector and return selection over channel. channel is unused
	// if emulation is a debugging emulation, in which case the 'chan string'
	// can be nil
	ReqROMSelector FeatureReq = "ReqROMSelector" // nil

	// request for a comparison window to be opened
	ReqComparison FeatureReq = "ReqComparison" // chan *image.RGBA, chan *image.RGBA

	// request for a bot window to be opened
	ReqBotFeedback FeatureReq = "ReqBotFeedback" // bots.Feedback
)

// Sentinal error returned if GUI does no support requested feature.
const (
	UnsupportedGuiFeature = "unsupported gui feature: %v"
)
