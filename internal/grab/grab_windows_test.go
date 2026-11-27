//go:build windows

package grab

import (
	"testing"
	"unsafe"
)

// SendInput rejects the whole call when cbSize is not sizeof(INPUT).
func TestInputStructSize(t *testing.T) {
	want := uintptr(40) // amd64 and arm64: DWORD type, 4 padding, 32-byte union
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 28 // 386: DWORD type, 24-byte union
	}
	if got := unsafe.Sizeof(input{}); got != want {
		t.Fatalf("unsafe.Sizeof(input{}) = %d, want %d", got, want)
	}
	if got := unsafe.Offsetof(input{}.ki); got != unsafe.Sizeof(uintptr(0)) {
		t.Errorf("offset of the union = %d, want %d", got, unsafe.Sizeof(uintptr(0)))
	}
}
