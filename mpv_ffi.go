//go:build (darwin || linux || windows) && (amd64 || arm64)

package ntvideo

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"github.com/go-webgpu/goffi/ffi"
	"github.com/go-webgpu/goffi/types"
)

// libmpv's stable C ABI is described in include/mpv/client.h and render.h.
// All calls are synchronous; arguments remain live through CallFunction. No Go
// callbacks or pointers are retained by libmpv. The library outlives its renderer.
type mpvFunction struct {
	symbol unsafe.Pointer
	cif    types.CallInterface
}

func (f *mpvFunction) call(result unsafe.Pointer, args ...unsafe.Pointer) error {
	_, err := ffi.CallFunction(&f.cif, f.symbol, result, args)
	runtime.KeepAlive(args)
	if err != nil {
		return fmt.Errorf("ntvideo: libmpv ABI call failed: %w", err)
	}
	return nil
}
func (f *mpvFunction) integer(args ...unsafe.Pointer) (int32, error) {
	var result int32
	err := f.call(unsafe.Pointer(&result), args...)
	return result, err
}

type mpvLibrary struct {
	handle    unsafe.Pointer
	functions map[string]*mpvFunction
}

func libraryNames(path string) []string {
	if path != "" {
		return []string{path}
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{"/opt/homebrew/lib/libmpv.2.dylib", "/usr/local/lib/libmpv.2.dylib", "libmpv.2.dylib", "libmpv.dylib"}
	case "windows":
		return []string{"libmpv-2.dll", "mpv-2.dll", "libmpv.dll"}
	default:
		return []string{"libmpv.so.2", "libmpv.so.1", "libmpv.so"}
	}
}
func openMPV(path string) (*mpvLibrary, error) {
	var handle unsafe.Pointer
	var err error
	for _, name := range libraryNames(path) {
		handle, err = ffi.LoadLibrary(name)
		if err == nil {
			break
		}
	}
	if handle == nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	lib := &mpvLibrary{handle: handle, functions: make(map[string]*mpvFunction)}
	ptr, integer, void := types.PointerTypeDescriptor, types.SInt32TypeDescriptor, types.VoidTypeDescriptor
	specs := []struct {
		name   string
		result *types.TypeDescriptor
		args   []*types.TypeDescriptor
	}{
		{"mpv_create", ptr, nil}, {"mpv_initialize", integer, []*types.TypeDescriptor{ptr}},
		{"mpv_terminate_destroy", void, []*types.TypeDescriptor{ptr}},
		{"mpv_set_option_string", integer, []*types.TypeDescriptor{ptr, ptr, ptr}},
		{"mpv_set_property_string", integer, []*types.TypeDescriptor{ptr, ptr, ptr}},
		{"mpv_get_property", integer, []*types.TypeDescriptor{ptr, ptr, integer, ptr}},
		{"mpv_get_property_string", ptr, []*types.TypeDescriptor{ptr, ptr}},
		{"mpv_free", void, []*types.TypeDescriptor{ptr}},
		{"mpv_command", integer, []*types.TypeDescriptor{ptr, ptr}},
		{"mpv_wait_event", ptr, []*types.TypeDescriptor{ptr, types.DoubleTypeDescriptor}},
		{"mpv_error_string", ptr, []*types.TypeDescriptor{integer}},
		{"mpv_render_context_create", integer, []*types.TypeDescriptor{ptr, ptr, ptr}},
		{"mpv_render_context_update", types.UInt64TypeDescriptor, []*types.TypeDescriptor{ptr}},
		{"mpv_render_context_render", integer, []*types.TypeDescriptor{ptr, ptr}},
		{"mpv_render_context_report_swap", void, []*types.TypeDescriptor{ptr}},
		{"mpv_render_context_free", void, []*types.TypeDescriptor{ptr}},
	}
	for _, spec := range specs {
		f := new(mpvFunction)
		f.symbol, err = ffi.GetSymbol(handle, spec.name)
		if err == nil {
			err = ffi.PrepareCallInterface(&f.cif, types.DefaultCall, spec.result, spec.args)
		}
		if err != nil {
			lib.close()
			return nil, fmt.Errorf("ntvideo: incompatible libmpv: %w", err)
		}
		lib.functions[spec.name] = f
	}
	return lib, nil
}
func (l *mpvLibrary) close() {
	if l.handle != nil {
		_ = ffi.FreeLibrary(l.handle)
		l.handle = nil
	}
}
func (l *mpvLibrary) fn(name string) *mpvFunction { return l.functions[name] }
func cstring(s string) *byte                      { return &[]byte(s + "\x00")[0] }
func goString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	// libmpv returns NUL-terminated strings. Bound malformed ABI responses.
	var out strings.Builder
	for i := 0; i < 1<<20; i++ {
		b := *(*byte)(unsafe.Add(p, i))
		if b == 0 {
			break
		}
		out.WriteByte(b)
	}
	return out.String()
}
func (l *mpvLibrary) err(code int32, callErr ...error) error {
	if len(callErr) > 0 && callErr[0] != nil {
		return callErr[0]
	}
	if code >= 0 {
		return nil
	}
	var p unsafe.Pointer
	if err := l.fn("mpv_error_string").call(unsafe.Pointer(&p), unsafe.Pointer(&code)); err != nil {
		return err
	}
	return fmt.Errorf("ntvideo: media backend: %s", goString(p))
}

type mpvEvent struct {
	ID, Error int32
	Userdata  uint64
	Data      unsafe.Pointer
}

// Stable enum values from libmpv's client.h and render.h.
const (
	mpvEventNone            = 0
	mpvEventEndFile         = 7
	mpvEventFileLoaded      = 8
	mpvEventSeek            = 20
	mpvEventPlaybackRestart = 21

	mpvRenderParamAPIType            = 1
	mpvRenderParamBlockForTargetTime = 12
	mpvRenderParamSWSize             = 17
	mpvRenderParamSWFormat           = 18
	mpvRenderParamSWStride           = 19
	mpvRenderParamSWPointer          = 20
)

type mpvEndFile struct{ Reason, Error int32 }
type mpvRenderParam struct {
	Kind int32
	Data unsafe.Pointer
}
