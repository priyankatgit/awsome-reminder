package wakehelper

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winmm waveIn capture: 16 kHz mono 16-bit, 4 buffers of 100 ms, signalled
// through an event (CALLBACK_EVENT), so the loop needs no callback thread.

const (
	waveMapper          = 0xFFFFFFFF // WAVE_MAPPER: the default capture device
	waveFormatPCM       = 1
	callbackEvent       = 0x00050000
	whdrDone            = 0x00000001
	mmsyserrNoError     = 0
	mmsyserrBadDeviceID = 2

	micRate    = 16000
	micBuffers = 4
	micBufLen  = micRate / 10 // samples per buffer (100 ms)
	// No buffer for this long means the device went away (unplugged, or
	// taken by an exclusive-mode app): exit so the supervisor restarts us.
	micStallMs = 5000
)

var (
	winmm                 = windows.NewLazySystemDLL("winmm.dll")
	procWaveInGetNumDevs  = winmm.NewProc("waveInGetNumDevs")
	procWaveInOpen        = winmm.NewProc("waveInOpen")
	procWaveInClose       = winmm.NewProc("waveInClose")
	procWaveInPrepare     = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepare   = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer   = winmm.NewProc("waveInAddBuffer")
	procWaveInStart       = winmm.NewProc("waveInStart")
	procWaveInReset       = winmm.NewProc("waveInReset")
	procWaveInGetErrorTxt = winmm.NewProc("waveInGetErrorTextW")
)

// waveFormatEx mirrors WAVEFORMATEX (18 bytes; the fields are naturally
// aligned, so Go's trailing padding is the only difference).
type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

// waveHdr mirrors WAVEHDR (48 bytes on amd64).
type waveHdr struct {
	Data          uintptr // LPSTR
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

// errMicStopped is returned by mic.next when stop is signalled.
var errMicStopped = errors.New("stopped")

// micError is a winmm failure; its text is what the helper prints.
type micError struct{ text string }

func (e *micError) Error() string { return e.text }

func waveInGetNumDevs() int {
	if procWaveInGetNumDevs.Find() != nil {
		return 0
	}
	n, _, _ := procWaveInGetNumDevs.Call()
	return int(uint32(n))
}

// micErrorText is winmm's text for an MMRESULT, with the code as a fallback.
func micErrorText(code uintptr) string {
	if procWaveInGetErrorTxt.Find() == nil {
		buf := make([]uint16, 256) // MAXERRORLENGTH is 256
		r, _, _ := procWaveInGetErrorTxt.Call(code, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if r == mmsyserrNoError {
			if s := windows.UTF16ToString(buf); s != "" {
				return s
			}
		}
	}
	if code == mmsyserrBadDeviceID {
		return "no capture device (winmm error 2)"
	}
	return fmt.Sprintf("winmm error %d", code)
}

// mic is an open capture device. Its buffers and headers are Go memory that
// winmm writes from its own thread, so they are pinned until close.
type mic struct {
	h       uintptr // HWAVEIN
	event   windows.Handle
	stop    windows.Handle // signalled (by the stdin watcher) to end next
	hdrs    [micBuffers]waveHdr
	bufs    [micBuffers][micBufLen]int16
	nextBuf int // the buffer that completes next (they complete in order)
	out     []int16
	pin     runtime.Pinner
	queued  bool // headers prepared and handed to winmm
}

// openMic opens the default capture device and starts recording. When stop
// (an event) is signalled, next returns errMicStopped; 0 means no stop event.
func openMic(stop windows.Handle) (*mic, error) {
	if err := procWaveInOpen.Find(); err != nil {
		return nil, &micError{"winmm.dll: " + err.Error()}
	}
	ev, err := windows.CreateEvent(nil, 0, 0, nil) // auto-reset
	if err != nil {
		return nil, &micError{"CreateEvent: " + err.Error()}
	}
	m := &mic{event: ev, stop: stop, out: make([]int16, micBufLen)}
	m.pin.Pin(m)
	format := waveFormatEx{
		FormatTag:      waveFormatPCM,
		Channels:       1,
		SamplesPerSec:  micRate,
		AvgBytesPerSec: micRate * 2,
		BlockAlign:     2,
		BitsPerSample:  16,
	}
	r, _, _ := procWaveInOpen.Call(uintptr(unsafe.Pointer(&m.h)), waveMapper,
		uintptr(unsafe.Pointer(&format)), uintptr(ev), 0, callbackEvent)
	if r != mmsyserrNoError {
		m.pin.Unpin()
		windows.CloseHandle(ev)
		return nil, &micError{micErrorText(r)}
	}
	for i := range m.hdrs {
		h := &m.hdrs[i]
		h.Data = uintptr(unsafe.Pointer(&m.bufs[i][0]))
		h.BufferLength = micBufLen * 2
		if r, _, _ := procWaveInPrepare.Call(m.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h)); r != mmsyserrNoError {
			m.close()
			return nil, &micError{micErrorText(r)}
		}
		m.queued = true
		if r, _, _ := procWaveInAddBuffer.Call(m.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h)); r != mmsyserrNoError {
			m.close()
			return nil, &micError{micErrorText(r)}
		}
	}
	if r, _, _ := procWaveInStart.Call(m.h); r != mmsyserrNoError {
		m.close()
		return nil, &micError{micErrorText(r)}
	}
	return m, nil
}

// next returns the next 100 ms of audio (valid until the next call). It
// returns errMicStopped once stop is signalled, and a *micError if the
// device stops delivering audio.
func (m *mic) next() ([]int16, error) {
	handles := []windows.Handle{m.event}
	if m.stop != 0 {
		handles = append(handles, m.stop)
	}
	waited := uint32(0)
	for {
		h := &m.hdrs[m.nextBuf]
		if atomic.LoadUint32(&h.Flags)&whdrDone != 0 {
			n := int(atomic.LoadUint32(&h.BytesRecorded) / 2)
			out := m.out[:copy(m.out, m.bufs[m.nextBuf][:n])]
			if r, _, _ := procWaveInAddBuffer.Call(m.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h)); r != mmsyserrNoError {
				return nil, &micError{micErrorText(r)}
			}
			m.nextBuf = (m.nextBuf + 1) % micBuffers
			return out, nil
		}
		const step = 1000
		ev, err := windows.WaitForMultipleObjects(handles, false, step)
		switch {
		case err != nil:
			return nil, &micError{"WaitForMultipleObjects: " + err.Error()}
		case ev == windows.WAIT_OBJECT_0+1:
			return nil, errMicStopped
		case ev == uint32(windows.WAIT_TIMEOUT):
			if waited += step; waited >= micStallMs {
				return nil, &micError{"the microphone stopped delivering audio"}
			}
		default:
			waited = 0 // a buffer (or open/close) event: check the next header
		}
	}
}

// close stops recording and releases the device: waveInReset returns every
// queued buffer, then the headers are unprepared before waveInClose.
func (m *mic) close() {
	if m.h != 0 {
		procWaveInReset.Call(m.h)
		if m.queued {
			for i := range m.hdrs {
				procWaveInUnprepare.Call(m.h, uintptr(unsafe.Pointer(&m.hdrs[i])), unsafe.Sizeof(m.hdrs[i]))
			}
		}
		procWaveInClose.Call(m.h)
		m.h = 0
	}
	windows.CloseHandle(m.event)
	m.pin.Unpin()
}
