//go:build windows

package clipboardimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"unsafe"

	"github.com/inhere/suga/internal/i18n"
	"golang.org/x/sys/windows"
)

// Windows clipboard reading over user32/kernel32: Windows截图 puts a
// registered "PNG" format on the clipboard (bits as-is), most other apps a
// CF_DIB/CF_DIBV5 bitmap that is converted to PNG here so the view_image
// pipeline sees a picture it understands.

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClip          = user32.NewProc("OpenClipboard")
	procCloseClip         = user32.NewProc("CloseClipboard")
	procFormatAvail       = user32.NewProc("IsClipboardFormatAvailable")
	procGetData           = user32.NewProc("GetClipboardData")
	procRegFormat         = user32.NewProc("RegisterClipboardFormatW")
	procReadProcessMemory = kernel32.NewProc("ReadProcessMemory")
	procGlobalSize        = kernel32.NewProc("GlobalSize")
)

const (
	cfDIB   = 8
	cfDIBV5 = 17
	// maxDIBPixels bounds a converted bitmap like the view_image pixel limit:
	// a header claiming gigabytes is refused before any conversion.
	maxDIBPixels = 40_000_000
)

// platformReader reads the picture of this process' clipboard in the order a
// screenshot writer tends to fill it: registered PNG, then the raw bitmaps. tr
// renders the reported name and the failure reasons.
func platformReader(tr i18n.Translator) ImageReader {
	return func() ([]byte, string, error) { return readWindowsClipboard(tr) }
}

func readWindowsClipboard(tr i18n.Translator) ([]byte, string, error) {
	t := trOrFallback(tr)
	pngFormat := registeredPNGFormat()
	available := func(format uintptr) bool {
		r, _, _ := procFormatAvail.Call(format)
		return r != 0
	}
	var formats []struct {
		id  uint32
		png bool
	}
	if pngFormat != 0 && available(uintptr(pngFormat)) {
		formats = append(formats, struct {
			id  uint32
			png bool
		}{pngFormat, true})
	}
	if available(cfDIBV5) {
		formats = append(formats, struct {
			id  uint32
			png bool
		}{cfDIBV5, false})
	}
	if available(cfDIB) {
		formats = append(formats, struct {
			id  uint32
			png bool
		}{cfDIB, false})
	}
	if len(formats) == 0 {
		return nil, "", ErrNoClipboardImage
	}
	r0, _, _ := procOpenClip.Call(0)
	if r0 == 0 {
		return nil, "", errors.New(t.T("host.clipboard.open_failed"))
	}
	defer procCloseClip.Call()
	var lastErr error
	for _, format := range formats {
		handle, _, _ := procGetData.Call(uintptr(format.id))
		if handle == 0 {
			lastErr = errors.New(t.T("host.clipboard.format_failed", map[string]any{"format": format.id}))
			continue
		}
		data, err := globalBytes(tr, handle)
		if err != nil {
			lastErr = err
			continue
		}
		if format.png {
			return data, t.T("host.clipboard.name"), nil
		}
		converted, err := dibToPNG(tr, data)
		if err != nil {
			lastErr = err
			continue
		}
		return converted, t.T("host.clipboard.name"), nil
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", ErrNoClipboardImage
}

// registeredPNGFormat is the clipboard format id apps register under the name
// "PNG"; every of their writes is a PNG body.
func registeredPNGFormat() uint32 {
	name, err := windows.UTF16PtrFromString("PNG")
	if err != nil {
		return 0
	}
	r, _, _ := procRegFormat.Call(uintptr(unsafe.Pointer(name)))
	return uint32(r)
}

// globalBytes copies a global clipboard handle out, bounded by its GlobalSize.
// The bytes are copied with ReadProcessMemory on the current process instead of
// a GlobalLock dereference: a syscall keeps the handle so the garbage collector
// cannot take it, and no *byte from uintptr around it has to be built at all,
// which keeps go vet's unsafeptr check clean by construction.
func globalBytes(tr i18n.Translator, handle uintptr) ([]byte, error) {
	t := trOrFallback(tr)
	r, _, _ := procGlobalSize.Call(handle)
	if r == 0 {
		return nil, errors.New(t.T("host.clipboard.size_unknown"))
	}
	if r > maxClipboardBytes {
		return nil, errors.New(t.T("host.clipboard.too_large", map[string]any{"bytes": r, "limit": maxClipboardBytes}))
	}
	buf := make([]byte, r)
	var read uintptr
	current := windows.CurrentProcess()
	r, _, _ = procReadProcessMemory.Call(uintptr(current), handle,
		uintptr(unsafe.Pointer(&buf[0])), r, uintptr(unsafe.Pointer(&read)))
	if r == 0 {
		return nil, errors.New(t.T("host.clipboard.read_failed"))
	}
	return buf, nil
}

// dibToPNG converts a CF_DIB/CF_DIBV5 BITMAPINFO blob into bytes. Only the
// ranges Windows screenshots actually write are handled: 24/32-bit rows,
// BI_RGB compression (32-bit BITFIELDS is layout-compatible); anything rarer
// still leaves the PNG format above or is reported.
func dibToPNG(tr i18n.Translator, data []byte) ([]byte, error) {
	t := trOrFallback(tr)
	if len(data) < 40 {
		return nil, errors.New(t.T("host.clipboard.header_incomplete"))
	}
	header := binary.LittleEndian.Uint32(data[0:4])
	width := int32(binary.LittleEndian.Uint32(data[4:8]))
	height := int32(binary.LittleEndian.Uint32(data[8:12]))
	bpp := binary.LittleEndian.Uint16(data[14:16])
	compression := binary.LittleEndian.Uint32(data[16:20])
	topDown := height < 0
	h := int(height)
	if h < 0 {
		h = -h
	}
	w := int(width)
	if w <= 0 || h <= 0 || w*h > maxDIBPixels {
		return nil, errors.New(t.T("host.clipboard.bad_size", map[string]any{"width": w, "height": h}))
	}
	if compression != 0 && !(compression == 3 && bpp == 32) {
		return nil, errors.New(t.T("host.clipboard.bad_compression", map[string]any{"compression": compression}))
	}
	if bpp != 32 && bpp != 24 {
		return nil, errors.New(t.T("host.clipboard.bad_depth", map[string]any{"bpp": bpp}))
	}
	rowBytes := (int(bpp)*w + 31) / 32 * 4
	pixels := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := y
		if !topDown {
			row = h - 1 - y // a DIB stores its rows bottom-up unless signed negative
		}
		base := int(header) + row*rowBytes
		for x := 0; x < w; x++ {
			o := base + x*int(bpp)/8
			if o+int(bpp)/8 > len(data) {
				break
			}
			if bpp == 32 {
				a := data[o+3]
				if compression == 0 && a == 0 {
					a = 0xff // plain BI_RGB 32bpp is BGRA without alpha in practice
				}
				pixels.SetRGBA(x, y, color.RGBA{R: data[o+2], G: data[o+1], B: data[o], A: a})
			} else {
				pixels.SetRGBA(x, y, color.RGBA{R: data[o+2], G: data[o+1], B: data[o], A: 0xff})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, pixels); err != nil {
		return nil, errors.New(t.T("host.clipboard.convert_failed", map[string]any{"err": err}))
	}
	return buf.Bytes(), nil
}
