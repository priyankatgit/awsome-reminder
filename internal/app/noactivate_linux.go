package app

/*
#cgo linux pkg-config: gtk4
#include <gtk/gtk.h>
#ifdef GDK_WINDOWING_X11
#include <gdk/x11/gdkx.h>

// noFocusKey marks a window mapped by mapWithoutFocus (GObject data).
static const char *noFocusKey = "awesome-reminder-no-focus";

// x11Surface returns window's X11 surface, realizing it if needed, or NULL
// when the display is not X11 (Wayland). Main thread only.
static GdkSurface *x11Surface(void *window) {
	GtkWidget *w = GTK_WIDGET(window);
	gtk_widget_realize(w);
	GdkSurface *s = gtk_native_get_surface(GTK_NATIVE(w));
	if (s != NULL && GDK_IS_X11_SURFACE(s)) {
		return s;
	}
	return NULL;
}
#endif

// mapWithoutFocus maps window with _NET_WM_USER_TIME 0, which asks an X11
// window manager not to focus it on map (EWMH; GTK 3's focus-on-map=FALSE
// did the same), and returns TRUE. It does nothing and returns FALSE when
// not on X11. Unlike gtk_window_present it sends no activation request.
// Main thread only.
static gboolean mapWithoutFocus(void *window) {
#ifdef GDK_WINDOWING_X11
	GdkSurface *s = x11Surface(window);
	if (s == NULL) {
		return FALSE;
	}
	gdk_x11_surface_set_user_time(s, 0);
	g_object_set_data(G_OBJECT(window), noFocusKey, GINT_TO_POINTER(1));
	gtk_widget_set_visible(GTK_WIDGET(window), TRUE);
	return TRUE;
#else
	return FALSE;
#endif
}

// restoreUserTime undoes mapWithoutFocus, if it ran since the last call:
// window gets back the user time GTK would set on map (the display's last
// user event). With no user event seen yet the zero stays; the activation
// request of gtk_window_present still focuses the window then.
// Main thread only.
static void restoreUserTime(void *window) {
#ifdef GDK_WINDOWING_X11
	if (g_object_get_data(G_OBJECT(window), noFocusKey) == NULL) {
		return;
	}
	g_object_set_data(G_OBJECT(window), noFocusKey, NULL);
	GdkSurface *s = x11Surface(window);
	if (s == NULL) {
		return;
	}
	guint32 t = gdk_x11_display_get_user_time(gdk_surface_get_display(s));
	if (t != 0) {
		gdk_x11_surface_set_user_time(s, t);
	}
#endif
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// showNoActivate shows win without giving it keyboard focus where the OS
// allows it. win must be an always-on-top window (the capture box and the
// alert popup are).
//
// Wails v3.1.0 uses GTK 4, which dropped gtk_window_set_accept_focus and
// focus-on-map, and its Show is gtk_window_present, which also asks the
// window manager to activate the window. So on X11 the window (NativeWindow
// is its GtkWindow) is mapped here with a zero user time and no activation
// request, and the always-on-top state Wails re-applies after its own show
// is re-applied through SetAlwaysOnTop. Wails' visibility stays consistent:
// IsVisible reads the widget's visibility, Hide hides the widget and Show
// presents it. A later normal show calls restoreActivation first.
//
// Wayland has no way to show a window without focus, so there (and if the
// native window does not exist yet) it falls back to Show; the capture
// page's keyboard guard then keeps typing out of the box until a click, the
// hotkey or Ctrl+M.
func showNoActivate(win *application.WebviewWindow) (prevForeground uintptr) {
	h := win.NativeWindow()
	mapped := false
	if h != nil {
		application.InvokeSync(func() { mapped = C.mapWithoutFocus(h) != 0 })
	}
	if !mapped {
		win.Show()
		return 0
	}
	win.SetAlwaysOnTop(true)
	return 0
}

// restoreActivation undoes showNoActivate's zero user time before a normal,
// focusing show, so the window manager treats that map like any other.
func restoreActivation(win *application.WebviewWindow) {
	if h := win.NativeWindow(); h != nil {
		application.InvokeSync(func() { C.restoreUserTime(h) })
	}
}

// hideRestoringFocus hides win; the window manager picks the next active
// window.
func hideRestoringFocus(win *application.WebviewWindow, _ uintptr) {
	win.Hide()
}
