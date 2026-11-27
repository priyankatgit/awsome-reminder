//go:build darwin && cgo

package app

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// setDockBadge sets the Dock tile's badge ("" removes it). It does not wait
// for the main thread. label is copied before returning.
static void setDockBadge(const char *label) {
	@autoreleasepool {
		NSString *s = [NSString stringWithUTF8String:label];
		dispatch_async(dispatch_get_main_queue(), ^{
			[[NSApp dockTile] setBadgeLabel:([s length] > 0 ? s : nil)];
		});
	}
}
*/
import "C"

import (
	"sync"
	"unsafe"

	"awesome-reminder/internal/badge"
)

// macOS: the count is the Dock badge (NSApp.dockTile.badgeLabel). The app
// has no AppUserModelID equivalent to set.

func setAppUserModelID() {}

// newTaskbar returns the Dock badge. The Dock has no click to wire here:
// clicking the Dock icon is handled by Wails.
func newTaskbar(func()) taskbar { return &dockBadge{} }

type dockBadge struct {
	mu     sync.Mutex
	closed bool
}

func (d *dockBadge) SetCount(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		setDock(badge.Label(n))
	}
}

// Close clears the badge; later SetCount calls do nothing.
func (d *dockBadge) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		setDock("")
	}
}

func setDock(label string) {
	cs := C.CString(label)
	defer C.free(unsafe.Pointer(cs))
	C.setDockBadge(cs)
}
