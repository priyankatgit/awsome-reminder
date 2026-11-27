//go:build darwin && cgo

package grab

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices
#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <string.h>

// Manual reference counting: cgo does not turn on ARC. Every char * these
// functions return is malloc'd (or NULL) and the caller frees it.

static char *dupString(NSString *s) {
	if (s == nil) return NULL;
	const char *u = [s UTF8String];
	return u ? strdup(u) : NULL;
}

static int grabTrusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

// frontApp is the frontmost app's localized name; *pid is its pid, or 0.
static char *frontApp(int *pid) {
	*pid = 0;
	@autoreleasepool {
		NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
		if (app == nil) return NULL;
		*pid = (int)[app processIdentifier];
		return dupString([app localizedName]);
	}
}

// copyAXElement is el's attr as an element the caller releases, or NULL.
static AXUIElementRef copyAXElement(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || v == NULL) return NULL;
	if (CFGetTypeID(v) != AXUIElementGetTypeID()) {
		CFRelease(v);
		return NULL;
	}
	AXUIElementSetMessagingTimeout((AXUIElementRef)v, 0.25);
	return (AXUIElementRef)v;
}

// copyAXString is el's attr as a string, or NULL. Call inside a pool.
static char *copyAXString(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || v == NULL) return NULL;
	char *s = NULL;
	if (CFGetTypeID(v) == CFStringGetTypeID()) s = dupString((NSString *)v);
	CFRelease(v);
	return s;
}

// axInfo reads app pid's focused window title and its focused element's
// selected text; either is left NULL when it cannot be read.
static void axInfo(int pid, char **title, char **selected) {
	*title = NULL;
	*selected = NULL;
	if (pid <= 0) return;
	@autoreleasepool {
		AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
		if (app == NULL) return;
		// A hung app would otherwise block each call for about 6 s.
		AXUIElementSetMessagingTimeout(app, 0.25);
		AXUIElementRef win = copyAXElement(app, kAXFocusedWindowAttribute);
		if (win != NULL) {
			*title = copyAXString(win, kAXTitleAttribute);
			CFRelease(win);
		}
		AXUIElementRef focused = copyAXElement(app, kAXFocusedUIElementAttribute);
		if (focused != NULL) {
			*selected = copyAXString(focused, kAXSelectedTextAttribute);
			CFRelease(focused);
		}
		CFRelease(app);
	}
}

static int modifiersHeld(void) {
	CGEventFlags f = CGEventSourceFlagsState(kCGEventSourceStateHIDSystemState);
	return (f & (kCGEventFlagMaskCommand | kCGEventFlagMaskShift |
		kCGEventFlagMaskAlternate | kCGEventFlagMaskControl)) != 0;
}

static long pbChangeCount(void) {
	@autoreleasepool {
		return (long)[[NSPasteboard generalPasteboard] changeCount];
	}
}

static char *pbString(void) {
	@autoreleasepool {
		return dupString([[NSPasteboard generalPasteboard] stringForType:NSPasteboardTypeString]);
	}
}

// pbTypes is every item's types, one per line.
static char *pbTypes(void) {
	@autoreleasepool {
		NSMutableArray *all = [NSMutableArray array];
		for (NSPasteboardItem *item in [[NSPasteboard generalPasteboard] pasteboardItems]) {
			[all addObjectsFromArray:[item types]];
		}
		return dupString([all componentsJoinedByString:@"\n"]);
	}
}

// pbSave copies every item's data for every type into new items, and
// returns them as a retained NSArray for pbRestore or pbFree. When a type
// cannot be read it returns NULL and sets *bad to that type.
static void *pbSave(char **bad) {
	*bad = NULL;
	@autoreleasepool {
		NSMutableArray *saved = [[NSMutableArray alloc] init];
		for (NSPasteboardItem *item in [[NSPasteboard generalPasteboard] pasteboardItems]) {
			NSPasteboardItem *clone = [[[NSPasteboardItem alloc] init] autorelease];
			for (NSString *type in [item types]) {
				NSData *data = [item dataForType:type];
				if (data == nil) {
					*bad = dupString(type);
					[saved release];
					return NULL;
				}
				[clone setData:data forType:type];
			}
			[saved addObject:clone];
		}
		return saved;
	}
}

static void pbFree(void *saved) { [(NSArray *)saved release]; }

// pbRestore puts the saved items back, marked transient so clipboard
// managers skip the restore, and releases them. It returns 0 on failure.
static int pbRestore(void *saved) {
	int ok = 1;
	@autoreleasepool {
		NSArray *items = (NSArray *)saved;
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		if ([items count] > 0) {
			[[items objectAtIndex:0] setData:[NSData data] forType:@"org.nspasteboard.TransientType"];
			ok = [pb writeObjects:items] ? 1 : 0;
		}
		[items release];
	}
	return ok;
}

// postCopy posts Cmd+C (kVK_ANSI_C down and up). It returns 0 when the
// events cannot be made.
static int postCopy(void) {
	CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
	CGEventRef down = CGEventCreateKeyboardEvent(src, (CGKeyCode)8, true);
	CGEventRef up = CGEventCreateKeyboardEvent(src, (CGKeyCode)8, false);
	int ok = down != NULL && up != NULL;
	if (ok) {
		CGEventSetFlags(down, kCGEventFlagMaskCommand);
		CGEventSetFlags(up, kCGEventFlagMaskCommand);
		CGEventPost(kCGHIDEventTap, down);
		CGEventPost(kCGHIDEventTap, up);
	}
	if (down != NULL) CFRelease(down);
	if (up != NULL) CFRelease(up);
	if (src != NULL) CFRelease(src);
	return ok;
}
*/
import "C"

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// On macOS the grab first asks Accessibility for the focused element's
// selected text, which needs no injection. When that is empty it posts
// Cmd+C and reads the pasteboard, with the Windows safety rules: every
// pasteboard item is saved and put back, and nothing is injected when the
// pasteboard holds files or images or a type that cannot be read, into a
// terminal or IDE, or while a modifier is still held. Without the
// Accessibility permission only the fallback (the pasteboard's own text)
// is used. A copy that lands after the 150 ms wait is still put back by a
// background goroutine, as on Windows.
//
// AppKit's pasteboard and NSWorkspace are used from the calling goroutine,
// not the main thread: each C call has its own autorelease pool.

// restoreMu is held from the save until the pasteboard is put back,
// including by a background restore. A grab that finds it held does not
// inject.
var restoreMu sync.Mutex

// Grab returns the text selected in the frontmost app, and the app's
// localized name and focused window title. keys are ignored. It never
// panics.
func Grab(ctx context.Context, keys []Key) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("grab: panic: %v", r)
		}
	}()
	var pid C.int
	res.Process = goString(C.frontApp(&pid))
	if !AccessibilityTrusted() {
		res.Text = fallbackText()
		return res
	}
	var title, selected *C.char
	C.axInfo(pid, &title, &selected)
	res.Title = goString(title)
	if text := goString(selected); text != "" {
		res.Text, res.Grabbed = text, true
		return res
	}
	switch {
	case ctx.Err() != nil:
		return res
	case noInjectMac(res.Process):
		res.Text = fallbackText()
		return res
	case !modifiersReleased():
		log.Printf("grab: a modifier is still held; not injecting")
		res.Text = fallbackText()
		return res
	case !restoreMu.TryLock():
		log.Printf("grab: the last grab's restore is still pending; not injecting")
		res.Text = fallbackText()
		return res
	}
	res.Text, res.Grabbed = copySelection(ctx)
	return res
}

// AccessibilityTrusted reports whether the app has the Accessibility
// permission, which the grab needs to read the selection.
func AccessibilityTrusted() bool { return C.grabTrusted() != 0 }

// copySelection posts Cmd+C and returns the copied text, or the fallback
// quote with grabbed false. The caller holds restoreMu; copySelection
// unlocks it, or hands it to the background restore.
func copySelection(ctx context.Context) (text string, grabbed bool) {
	handedOff := false
	defer func() {
		if !handedOff {
			restoreMu.Unlock()
		}
	}()
	fallback, _ := fallbackQuote(goString(C.pbString()))
	// Reject files and images before reading anything: a read can make the
	// source app render promised data.
	types := strings.FieldsFunc(goString(C.pbTypes()), func(r rune) bool { return r == '\n' })
	if !canRestoreTypes(types, func(string) bool { return true }) {
		return fallback, false
	}
	// pbSave is the readable half of canRestoreTypes: it fails on the first
	// type whose data cannot be read.
	var bad *C.char
	saved := C.pbSave(&bad)
	if saved == nil {
		log.Printf("grab: pasteboard type %q cannot be read; not injecting", goString(bad))
		return fallback, false
	}
	if ctx.Err() != nil {
		C.pbFree(saved)
		return fallback, false
	}
	seq := changeCount()
	if C.postCopy() == 0 {
		log.Printf("grab: CGEventCreateKeyboardEvent failed; not injecting")
		C.pbFree(saved)
		return fallback, false
	}
	// From here the pasteboard must go back, whatever ctx says.
	if !waitForChange(changeCount, seq, seqTimeout, time.Now, time.Sleep) {
		handedOff = true
		go restoreLater(saved, seq)
		return fallback, false
	}
	text = goString(C.pbString())
	restore(saved)
	if text != "" {
		return text, true
	}
	return fallback, false
}

// restoreLater waits up to 1 s for a copy that missed the 150 ms wait and
// then puts the pasteboard back; if none lands, the pasteboard was never
// touched. It owns restoreMu and unlocks it.
func restoreLater(saved unsafe.Pointer, seq uint32) {
	defer restoreMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("grab: background restore: panic: %v", r)
		}
	}()
	if waitForChange(changeCount, seq, lateTimeout, time.Now, time.Sleep) {
		restore(saved)
		return
	}
	C.pbFree(saved)
}

// restore puts the saved items back and frees them.
func restore(saved unsafe.Pointer) {
	if C.pbRestore(saved) == 0 {
		log.Printf("grab: NSPasteboard writeObjects failed; pasteboard not restored")
	}
}

// modifiersReleased waits up to 300 ms for Cmd, Shift, Option and Control
// to be let go, so the copy does not arrive as Cmd+Shift+C.
func modifiersReleased() bool {
	deadline := time.Now().Add(releaseTimeout)
	for C.modifiersHeld() != 0 {
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
	return true
}

func changeCount() uint32 { return uint32(C.pbChangeCount()) }

// fallbackText is the fallback quote from the pasteboard's current text.
func fallbackText() string {
	q, _ := fallbackQuote(goString(C.pbString()))
	return q
}

// goString copies and frees a malloc'd C string; NULL is "".
func goString(p *C.char) string {
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}
