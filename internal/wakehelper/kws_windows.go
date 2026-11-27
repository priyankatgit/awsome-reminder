package wakehelper

import (
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The sherpa-onnx v2.0.3 C API (sherpa-onnx/c-api/c-api.h), called through
// golang.org/x/sys/windows without cgo. On amd64 every function is the one
// Windows x64 calling convention, and every argument and result we use is
// an integer or a pointer. Structs mirror the header field for field:
// uintptr for const char*, int32 for int32_t, float32 for float.

const kwsDLL = "sherpa-onnx-c-api.dll"

type onlineTransducerModelConfig struct{ Encoder, Decoder, Joiner uintptr }
type onlineParaformerModelConfig struct{ Encoder, Decoder uintptr }
type onlineOneModelConfig struct{ Model uintptr } // Zipformer2Ctc, NemoCtc, ToneCtc

type onlineModelConfig struct {
	Transducer    onlineTransducerModelConfig
	Paraformer    onlineParaformerModelConfig
	Zipformer2Ctc onlineOneModelConfig
	Tokens        uintptr
	NumThreads    int32
	Provider      uintptr
	Debug         int32
	ModelType     uintptr
	ModelingUnit  uintptr
	BpeVocab      uintptr
	TokensBuf     uintptr
	TokensBufSize int32
	NemoCtc       onlineOneModelConfig
	TOneCtc       onlineOneModelConfig
}

type featureConfig struct {
	SampleRate int32
	FeatureDim int32
}

// kwsConfig is SherpaOnnxKeywordSpotterConfig.
type kwsConfig struct {
	Feat              featureConfig
	Model             onlineModelConfig
	MaxActivePaths    int32
	NumTrailingBlanks int32
	KeywordsScore     float32
	KeywordsThreshold float32
	KeywordsFile      uintptr
	KeywordsBuf       uintptr
	KeywordsBufSize   int32
}

// kwsResult is SherpaOnnxKeywordResult.
type kwsResult struct {
	Keyword    uintptr
	Tokens     uintptr
	TokensArr  uintptr
	Count      int32
	Timestamps uintptr
	StartTime  float32
	JSON       uintptr
}

type modelPaths struct {
	Encoder, Decoder, Joiner, Tokens, Keywords string
}

// kws is a loaded keyword spotter with one stream.
type kws struct {
	spotter, stream uintptr
	strs            []*byte // C strings in the config, kept for the spotter's lifetime
	samples         []float32

	createStream, destroyStream, destroySpotter uintptr
	accept, isReady, decode, reset              uintptr
	getResult, destroyResult                    uintptr
}

// loadKWS loads the C API from libDir (onnxruntime.dll resolves from the
// same folder) and creates the spotter and its stream with the stock
// program's settings.
func loadKWS(libDir string, m modelPaths, threads int) (*kws, error) {
	dll, err := filepath.Abs(filepath.Join(libDir, kwsDLL))
	if err != nil {
		return nil, err
	}
	h, err := windows.LoadLibraryEx(dll, 0,
		windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", dll, err)
	}
	// The library stays loaded for the life of the process.
	k := &kws{}
	var create uintptr
	procs := []struct {
		p    *uintptr
		name string
	}{
		{&create, "SherpaOnnxCreateKeywordSpotter"},
		{&k.destroySpotter, "SherpaOnnxDestroyKeywordSpotter"},
		{&k.createStream, "SherpaOnnxCreateKeywordStream"},
		{&k.destroyStream, "SherpaOnnxDestroyOnlineStream"},
		{&k.accept, "SherpaOnnxOnlineStreamAcceptWaveform"},
		{&k.isReady, "SherpaOnnxIsKeywordStreamReady"},
		{&k.decode, "SherpaOnnxDecodeKeywordStream"},
		{&k.reset, "SherpaOnnxResetKeywordStream"},
		{&k.getResult, "SherpaOnnxGetKeywordResult"},
		{&k.destroyResult, "SherpaOnnxDestroyKeywordResult"},
	}
	for _, p := range procs {
		a, err := windows.GetProcAddress(h, p.name)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", kwsDLL, p.name, err)
		}
		*p.p = a
	}

	str := func(s string) uintptr {
		if s == "" {
			return 0 // unused strings are NULL
		}
		b, err := windows.BytePtrFromString(s)
		if err != nil {
			return 0
		}
		k.strs = append(k.strs, b)
		return uintptr(unsafe.Pointer(b))
	}
	var c kwsConfig // zeroed: every unused field is 0 or NULL
	c.Feat = featureConfig{SampleRate: micRate, FeatureDim: 80}
	c.Model.Transducer = onlineTransducerModelConfig{str(m.Encoder), str(m.Decoder), str(m.Joiner)}
	c.Model.Tokens = str(m.Tokens)
	c.Model.NumThreads = int32(threads)
	c.Model.Provider = str("cpu")
	c.MaxActivePaths = 4    // stock default
	c.NumTrailingBlanks = 1 // stock default
	c.KeywordsScore = 1.0   // overridden per line by ":N"
	c.KeywordsThreshold = 0.25
	c.KeywordsFile = str(m.Keywords)

	r, _, _ := syscall.SyscallN(create, uintptr(unsafe.Pointer(&c)))
	runtime.KeepAlive(&c)
	runtime.KeepAlive(k.strs)
	if r == 0 {
		return nil, fmt.Errorf("the keyword spotter could not be created from %s", filepath.Dir(m.Encoder))
	}
	k.spotter = r
	if err := k.newStream(); err != nil {
		k.close()
		return nil, err
	}
	return k, nil
}

func (k *kws) newStream() error {
	s, _, _ := syscall.SyscallN(k.createStream, k.spotter)
	if s == 0 {
		return fmt.Errorf("the keyword stream could not be created")
	}
	k.stream = s
	return nil
}

// restart replaces the stream with a fresh one: decoder state, buffered
// features and timestamps all start over, so the next speech burst is not
// joined to the end of the last one.
func (k *kws) restart() error {
	syscall.SyscallN(k.destroyStream, k.stream)
	k.stream = 0
	return k.newStream()
}

// feed converts block to float samples in [-1, 1], hands them to the stream
// and decodes what is ready. Each detection's JSON goes to onDetect, and
// the stream is reset after it, as the stock program does.
func (k *kws) feed(block []int16, onDetect func(json string)) {
	if len(block) == 0 {
		return
	}
	k.samples = k.samples[:0]
	for _, s := range block {
		k.samples = append(k.samples, float32(s)/32768)
	}
	syscall.SyscallN(k.accept, k.stream, micRate, uintptr(unsafe.Pointer(&k.samples[0])), uintptr(len(k.samples)))
	runtime.KeepAlive(k.samples)
	for {
		ready, _, _ := syscall.SyscallN(k.isReady, k.spotter, k.stream)
		if int32(ready) == 0 {
			return
		}
		syscall.SyscallN(k.decode, k.spotter, k.stream)
		rp, _, _ := syscall.SyscallN(k.getResult, k.spotter, k.stream)
		if rp == 0 {
			continue
		}
		res := (*kwsResult)(cPointer(rp))
		if kw := cString(res.Keyword); kw != "" {
			onDetect(cString(res.JSON))
			syscall.SyscallN(k.reset, k.spotter, k.stream)
		}
		syscall.SyscallN(k.destroyResult, rp)
	}
}

func (k *kws) close() {
	if k.stream != 0 {
		syscall.SyscallN(k.destroyStream, k.stream)
		k.stream = 0
	}
	if k.spotter != 0 {
		syscall.SyscallN(k.destroySpotter, k.spotter)
		k.spotter = 0
	}
	runtime.KeepAlive(k.strs)
}

// cPointer turns an address of C memory (never Go memory) into a pointer.
func cPointer(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

// cString copies a NUL-terminated C string.
func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	return windows.BytePtrToString((*byte)(cPointer(p)))
}
