package native

import (
	"encoding/binary"
	"image"
)

// JPEG EXIF orientation (design 0.3, "处理 JPEG EXIF 方向"): a phone or camera
// stores the sensor orientation in the APP1 segment instead of rotating the
// pixels, so the stored picture looks sideways. view_image rotates the decoded
// pixels to the orientation the EXIF declares before scaling, so the model sees
// the picture upright (and the reported size matches what it sees).
//
// Only the orientation tag (0x0112) of IFD0 is read: no EXIF library is pulled
// in, and a malformed segment simply means "orientation 1" (as stored).

// exifOrientation returns the EXIF orientation (1..8) of a JPEG, or 1 when the
// picture carries none (or has a segment this reader does not understand).
func exifOrientation(data []byte) int {
	tiff, ok := exifTIFF(data)
	if !ok {
		return 1
	}
	// TIFF header: byte order ("II" little endian, "MM" big endian), the 42
	// magic and the offset of IFD0.
	var order binary.ByteOrder
	switch string(tiff[0:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifd : ifd+2]))
	for i := 0; i < count; i++ {
		// One IFD entry: tag (2), type (2), count (4), value or offset (4).
		entry := ifd + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:entry+2]) != 0x0112 {
			continue
		}
		// The type is SHORT (3) with one value, held in the first two bytes of
		// the value field.
		value := int(order.Uint16(tiff[entry+8 : entry+10]))
		if value < 1 || value > 8 {
			return 1
		}
		return value
	}
	return 1
}

// exifTIFF returns the TIFF block of a JPEG's Exif APP1 segment. A segment
// chain that is cut short or is not Exif returns false.
func exifTIFF(data []byte) ([]byte, bool) {
	// SOI.
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, false
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return nil, false
		}
		marker := data[i+1]
		// Standalone markers (restart, TEM) carry no length; SOS starts the
		// entropy-coded data, i.e. no more metadata.
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 {
			return nil, false
		}
		length := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if length < 2 || i+2+length > len(data) {
			return nil, false
		}
		payload := data[i+4 : i+2+length]
		if marker == 0xE1 && len(payload) > 6 && string(payload[:6]) == "Exif\x00\x00" {
			return payload[6:], true
		}
		i += 2 + length
	}
	return nil, false
}

// applyOrientation returns img rotated or mirrored as the EXIF orientation
// says (1 = as stored).
func applyOrientation(img image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return img
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	// Orientations 5..8 transpose the picture, so the canvas swaps its sides.
	swapped := orientation >= 5
	outWidth, outHeight := width, height
	if swapped {
		outWidth, outHeight = height, width
	}
	dst := image.NewRGBA(image.Rect(0, 0, outWidth, outHeight))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var dx, dy int
			switch orientation {
			case 2: // mirror horizontal
				dx, dy = width-1-x, y
			case 3: // rotate 180
				dx, dy = width-1-x, height-1-y
			case 4: // mirror vertical
				dx, dy = x, height-1-y
			case 5: // transpose (mirror along the main diagonal)
				dx, dy = y, x
			case 6: // rotate 90° clockwise
				dx, dy = height-1-y, x
			case 7: // transverse (mirror along the anti-diagonal)
				dx, dy = height-1-y, width-1-x
			case 8: // rotate 90° counter-clockwise
				dx, dy = y, width-1-x
			}
			dst.Set(dx, dy, img.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return dst
}
