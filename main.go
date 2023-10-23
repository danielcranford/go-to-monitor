package main

import (
	"flag"
	"log"
	"math"

	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgbutil"
	"github.com/BurntSushi/xgbutil/ewmh"
	"github.com/BurntSushi/xgbutil/xinerama"
	"github.com/BurntSushi/xgbutil/xrect"
	"github.com/BurntSushi/xgbutil/xwindow"
)

type Oridinal int

const (
	North Oridinal = iota
	South
	East
	West
)

type RelativeGeometry struct {
	x, y, width, height float64
}

// Scales window geometry in integer units to fraction of screen in floating point units
// This is a hack that sort of deals with monitors that are different sizes.
// Moving a window that takes up a 1/4th of the screen to a monitor will resize the window to take 1/4 of the new monitor regardless of the actual monitor resolution and dimensions
func build_relative(geo xrect.Rect, container xrect.Rect) RelativeGeometry {
	return RelativeGeometry{
		x:      float64(geo.X()-container.X()) / float64(container.Width()),
		y:      float64(geo.Y()-container.Y()) / float64(container.Height()),
		width:  float64(geo.Width()) / float64(container.Width()),
		height: float64(geo.Height()) / float64(container.Height()),
	}
}

func build_absolute(rgeo RelativeGeometry, container xrect.Rect) xrect.Rect {
	return xrect.New(
		container.X()+int(rgeo.x*float64(container.Width())),
		container.Y()+int(rgeo.y*float64(container.Height())),
		int(rgeo.width*float64(container.Width())),
		int(rgeo.height*float64(container.Height())),
	)
}

func overlaps_y(r xrect.Rect, r2 xrect.Rect) bool {
	return r2.Y() < r.Y()+r.Height() && r2.Y()+r2.Height() > r.Y()
}

func overlaps_x(r xrect.Rect, r2 xrect.Rect) bool {
	return r2.X() < r.X()+r.Width() && r2.X()+r2.Width() > r.X()
}

// Scan list of screens to find the "next" screen in the given direction
func find_next(curr xrect.Rect, screens []xrect.Rect, dir Oridinal, wrap bool) xrect.Rect {
	// east/west, search x axis
	pos := xrect.Rect.X
	// only consider screens that have overlaping y dimensions
	overlaps := overlaps_y

	if dir == North || dir == South {
		// north/south, search y-axis
		pos = xrect.Rect.Y
		// only consider screens that have overlaping x dimensions
		overlaps = overlaps_x
	}

	i := 1
	var not_found xrect.Rect = xrect.New(math.MaxInt, math.MaxInt, 0, 0)
	// invert search direction for west or north
	if dir == West || dir == North {
		i = -1
		not_found = xrect.New(math.MinInt, math.MinInt, 0, 0)
	}
	next := not_found
	global_min := not_found

	for _, r := range screens {
		// skip curr and non-overlapping
		if r == curr || !overlaps(r, curr) {
			continue
		}

		// find first past curr
		if i*pos(r) > i*pos(curr) &&
			i*pos(r) < i*pos(next) {
			next = r
		}

		// find global miniumum (for wrapping support)
		if wrap && i*pos(r) < i*pos(global_min) {
			global_min = r
		}

	}

	if wrap && next == not_found {
		next = global_min
	}

	if next == not_found {
		next = curr
	}

	return next

}

// This should be in xbgutil
type EwmhClientSource int

const (
	Unknown EwmhClientSource = iota
	Application
	Pager
)

func WmStateReqExtra2(win xwindow.Window, action int, source EwmhClientSource,
	atoms ...string) error {

	var i int
	for i = 0; i < len(atoms)/2; i++ {
		// ewmh _NET_WM_STATE client message accepts 2 atoms at a time
		// unknown if a simple property update to the _NET_WM_STATE property is supported since ewmh specifies the _NET_WM_STATE value must be updated via the client message
		first := atoms[i*2]
		second := atoms[1*2+1]
		err := ewmh.WmStateReqExtra(win.X, win.Id, action, first, second, int(source))
		if err != nil {
			return err
		}
	}

	// Finish the tail
	if i*2 < len(atoms) {
		err := ewmh.WmStateReqExtra(win.X, win.Id, action, atoms[i*2], "", int(source))
		if err != nil {
			return err
		}
	}

	return nil
}

// xwindow.adjustSize has a bug where parent window is not retrieved
// adjustSize takes a client and dimensions, and adjust them so that they'll
// account for window decorations. For example, if you want a window to be
// 200 pixels wide, a window manager will typically determine that as
// you wanting the *client* to be 200 pixels wide. The end result is that
// the client plus decorations ends up being
// (200 + left decor width + right decor width) pixels wide. Which is probably
// not what you want. Therefore, transform 200 into
// 200 - decoration window width - client window width.
// Similarly for height.
func adjustSize(win xwindow.Window,
	w, h int) (int, int, error) {

	// raw client geometry
	cGeom, err := xwindow.RawGeometry(win.X, xproto.Drawable(win.Id))
	if err != nil {
		return 0, 0, err
	}

	// geometry with decorations
	decorations, err := DecorWindow(&win)
	if err != nil {
		return 0, 0, err
	}

	pGeom, err := xwindow.RawGeometry(win.X, xproto.Drawable(decorations.Id))
	if err != nil {
		return 0, 0, err
	}

	neww := w - (pGeom.Width() - cGeom.Width())
	newh := h - (pGeom.Height() - cGeom.Height())
	if neww < 1 {
		neww = 1
	}
	if newh < 1 {
		newh = 1
	}
	return neww, newh, nil
}

// xwindow.WMMoveResize has a bug where decorations are not accounted for
//
// WMMoveResize is an accurate means of resizing a window, accounting for
// decorations. Usually, the x,y coordinates are fine---we just need to
// adjust the width and height.
// This should be used when moving/resizing top-level client windows with
// reparenting window managers that support EWMH.
func WMMoveResize(w xwindow.Window, x, y, width, height int) error {
	neww, newh, err := adjustSize(w, width, height)
	if err != nil {
		return err
	}
	return ewmh.MoveresizeWindowExtra(w.X, w.Id, x, y, neww, newh,
		xproto.GravityBitForget, 2, true, true)
}

// Logic lifted from xwindow.DecorGeometry
func DecorWindow(w *xwindow.Window) (*xwindow.Window, error) {
	parent := w
	for {
		tempParent, err := parent.Parent()
		if err != nil || tempParent.Id == w.X.RootWin() {
			return parent, err
		}
		parent = tempParent
	}
}

func parseDir(dirStr string) Oridinal {
	switch dirStr[0] {
	case 'E':
		return East
	case 'W':
		return West
	case 'N':
		return North
	case 'S':
		return South
	default:
		return East
	}
}

func main() {
	var dirStr string
	var wrap bool
	flag.StringVar(&dirStr, "direction", "East", "direction to move (North, South, East, West)")
	flag.BoolVar(&wrap, "wrap", true, "enable wrapping")
	flag.Parse()

	X, err := xgbutil.NewConn()
	if err != nil {
		log.Fatalf("Error connecting to display: %v", err)
	}
	defer X.Conn().Close()

	active_window_id, err := ewmh.ActiveWindowGet(X)
	if err != nil {
		log.Fatalf("Error getting active window: %v", err)
	}

	active_window := xwindow.New(X, active_window_id)
	current_geometry, err := active_window.DecorGeometry()
	if err != nil {
		log.Fatalf("Error getting active window geometry: %v", err)
	}

	screens, err := xinerama.PhysicalHeads(X)
	if err != nil {
		log.Fatalf("Error getting list of monitors: %v", err)
	}

	// Find monitor with largest overlap
	index := xrect.LargestOverlap(current_geometry, screens)
	if index == -1 {
		log.Fatalf("Active window does not overlap any monitor")
	}
	screen_geometry := screens[index]
	next_screen := find_next(screen_geometry, screens, parseDir(dirStr), wrap)

	if next_screen == screen_geometry {
		// Nothing to do
		return
	}

	// Scale (if new screen is different size) and translate
	relative_geometry := build_relative(current_geometry, screen_geometry)
	next_geometry := build_absolute(relative_geometry, next_screen)

	// Retrieve properties that must be removed prior to moving
	// 3 NET_WM_STATE window properties prevent a windows from being moved across monitors:
	//'_NET_WM_STATE_MAXIMIZED_HORZ' '_NET_WM_STATE_MAXIMIZED_VERT', '_NET_WM_STATE_FULLSCREEN'
	state, err := ewmh.WmStateGet(X, active_window.Id)
	if err != nil {
		log.Fatalf("Unable to retrieve active window's state: %v", err)
	}
	to_remove := make([]string, len(state))
	for _, x := range state {
		if x == "_NET_WM_STATE_MAXIMIZED_HORZ" ||
			x == "_NET_WM_STATE_MAXIMIZED_VERT" ||
			x == "_NET_WM_STATE_FULLSCREEN" {
			to_remove = append(to_remove, x)
		}
	}

	err = WmStateReqExtra2(*active_window, ewmh.StateRemove, Pager, to_remove...)
	if err != nil {
		log.Fatalf("Unable to update _NET_WM_STATE to make window moveable: %v", err)
	}

	// Move window
	// TODO: xwindow.WMMoveResize has a bug in current version of xbgutil
	err = WMMoveResize(*active_window, next_geometry.X(), next_geometry.Y(), next_geometry.Width(), next_geometry.Height())
	if err != nil {
		log.Fatalf("Unable to move active window: %v", err)
	}

	// Restore maximized/fullscreen state
	err = WmStateReqExtra2(*active_window, ewmh.StateAdd, Pager, to_remove...)
	if err != nil {
		log.Fatalf("Unable to restore _NET_WM_STATE after moving window: %v", err)
	}
}
