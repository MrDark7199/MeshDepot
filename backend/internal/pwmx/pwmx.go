// Package pwmx reads Anycubic Photon Workshop files (.pwmx and the related resin
// formats).
//
// They contain no 3D geometry, but a stack of RLE-compressed 2D exposure images
// plus an embedded preview PNG, so the 3D view is reconstructed from the layer
// stack (see mesh.go).
//
// Format layout, read off a real Photon-Mono-X file, version 1:
//
//	FileMark:  "ANYCUBIC\0\0\0\0" (12B) | version u32 | areaNum u32 |
//	           then a u32 address per section, each padded to 8B
//	           (HEADER, PREVIEW, LAYERDEF, layer image data).
//	Section:   tag[8] | reserved u32 | tableLength u32 | body[tableLength]
//	HEADER:    pixelSizeUm f32, layerHeight f32, exposure f32, waitBeforeCure f32,
//	           bottomExposure f32, bottomLayers f32, liftHeight f32, liftSpeed f32,
//	           _ f32, volumeMl f32, antiAlias u32, resX u32, resY u32, weightG f32, …
//	LAYERDEF:  layerCount u32, then layerCount × 32B record
//	           (dataAddress u32, dataLength u32, liftHeight f32, liftSpeed f32,
//	            exposure f32, layerHeight f32, _ , _)
//	Layer-RLE: big-endian u16; Bits[15:12] = grayscale (0..15, ×17 → 0..255),
//	           Bits[11:0] = run length. Sum of runs = resX × resY.
package pwmx

import (
	"encoding/binary"
	"fmt"
	"math"
)

type Header struct {
	PixelSizeUm    float32
	LayerHeight    float32
	ExposureTime   float32
	LightOffTime   float32
	BottomExposure float32
	BottomLayers   float32
	LiftHeight     float32
	LiftSpeed      float32
	RetractSpeed   float32
	VolumeMl       float32
	AntiAliasing   uint32
	ResolutionX    uint32
	ResolutionY    uint32
	WeightG        float32
	Price          float32
}

type LayerDef struct {
	DataAddress  uint32
	DataLength   uint32
	LiftHeight   float32
	LiftSpeed    float32
	ExposureTime float32
	LayerHeight  float32
}

type Preview struct {
	Width  uint32
	Height uint32
	Data   []byte // RGB565, little-endian, Width*Height*2 bytes
}

type File struct {
	Version uint32
	Header  Header
	Preview Preview
	Layers  []LayerDef
	raw     []byte
}

const fileMark = "ANYCUBIC"

// Parse reads FileMark, HEADER, PREVIEW and LAYERDEF. The layer RLE stays in the
// raw buffer and is only decoded by DecodeLayer.
func Parse(data []byte) (*File, error) {
	if len(data) < 0x30 || string(data[:len(fileMark)]) != fileMark {
		return nil, fmt.Errorf("pwmx: no ANYCUBIC file mark")
	}
	file := &File{raw: data}
	file.Version = le32(data, 0x0C)

	headerAddress := le32(data, 0x14)
	previewAddress := le32(data, 0x1C)
	layerDefAddress := le32(data, 0x24)

	if failure := file.parseHeader(int(headerAddress)); failure != nil {
		return nil, failure
	}
	if previewAddress != 0 {
		if failure := file.parsePreview(int(previewAddress)); failure != nil {
			return nil, failure
		}
	}
	if failure := file.parseLayerDef(int(layerDefAddress)); failure != nil {
		return nil, failure
	}
	return file, nil
}

// ParseHeader reads the FileMark and HEADER only, so a prefix of the file is
// enough: reading the print settings must not force a multi-hundred-MB layer stack
// into memory, which full Parse would. The layout is shared by the whole Photon
// Workshop family - only the layer encoding differs, and that is not read here.
func ParseHeader(data []byte) (*Header, error) {
	if len(data) < 0x30 || string(data[:len(fileMark)]) != fileMark {
		return nil, fmt.Errorf("pwmx: no ANYCUBIC file mark")
	}
	file := &File{raw: data}
	if failure := file.parseHeader(int(le32(data, 0x14))); failure != nil {
		return nil, failure
	}
	return &file.Header, nil
}

// sectionBody checks the tag and returns the body's offset and length.
func (file *File) sectionBody(address int, tag string) (bodyOffset, bodyLength int, failure error) {
	if address < 0 || address+16 > len(file.raw) {
		return 0, 0, fmt.Errorf("pwmx: section %q outside the file", tag)
	}
	got := trimTag(file.raw[address : address+8])
	if got != tag {
		return 0, 0, fmt.Errorf("pwmx: expected section %q, found %q @%#x", tag, got, address)
	}
	length := int(le32(file.raw, address+12))
	bodyOffset = address + 16
	if bodyOffset+length > len(file.raw) {
		return 0, 0, fmt.Errorf("pwmx: section %q body outside the file", tag)
	}
	return bodyOffset, length, nil
}

func (file *File) parseHeader(address int) error {
	body, length, failure := file.sectionBody(address, "HEADER")
	if failure != nil {
		return failure
	}
	if length < 0x38 {
		return fmt.Errorf("pwmx: HEADER body too short (%d bytes)", length)
	}
	header := &file.Header
	header.PixelSizeUm = f32(file.raw, body+0x00)
	header.LayerHeight = f32(file.raw, body+0x04)
	header.ExposureTime = f32(file.raw, body+0x08)
	header.LightOffTime = f32(file.raw, body+0x0C)
	header.BottomExposure = f32(file.raw, body+0x10)
	header.BottomLayers = f32(file.raw, body+0x14)
	header.LiftHeight = f32(file.raw, body+0x18)
	header.LiftSpeed = f32(file.raw, body+0x1C)
	header.RetractSpeed = f32(file.raw, body+0x20)
	header.VolumeMl = f32(file.raw, body+0x24)
	header.AntiAliasing = le32(file.raw, body+0x28)
	header.ResolutionX = le32(file.raw, body+0x2C)
	header.ResolutionY = le32(file.raw, body+0x30)
	header.WeightG = f32(file.raw, body+0x34)
	// Price exists only in the longer HEADER tables of later versions.
	if length >= 0x3C {
		header.Price = f32(file.raw, body+0x38)
	}
	return nil
}

func (file *File) parsePreview(address int) error {
	body, _, failure := file.sectionBody(address, "PREVIEW")
	if failure != nil {
		return failure
	}
	// Preview sub-header: width u32, _ u32, height u32, then RGB565 data.
	width := le32(file.raw, body+0x00)
	height := le32(file.raw, body+0x08)
	dataOffset := body + 12
	requiredBytes := int(width) * int(height) * 2
	if dataOffset+requiredBytes > len(file.raw) {
		return fmt.Errorf("pwmx: PREVIEW data outside the file (%dx%d)", width, height)
	}
	file.Preview = Preview{Width: width, Height: height, Data: file.raw[dataOffset : dataOffset+requiredBytes]}
	return nil
}

func (file *File) parseLayerDef(address int) error {
	body, _, failure := file.sectionBody(address, "LAYERDEF")
	if failure != nil {
		return failure
	}
	count := int(le32(file.raw, body))
	recordStart := body + 4
	const recordSize = 32
	if recordStart+count*recordSize > len(file.raw) {
		return fmt.Errorf("pwmx: LAYERDEF (%d layers) outside the file", count)
	}
	file.Layers = make([]LayerDef, count)
	for index := 0; index < count; index++ {
		offset := recordStart + index*recordSize
		file.Layers[index] = LayerDef{
			DataAddress:  le32(file.raw, offset+0x00),
			DataLength:   le32(file.raw, offset+0x04),
			LiftHeight:   f32(file.raw, offset+0x08),
			LiftSpeed:    f32(file.raw, offset+0x0C),
			ExposureTime: f32(file.raw, offset+0x10),
			LayerHeight:  f32(file.raw, offset+0x14),
		}
	}
	return nil
}

// DecodeLayer returns resX*resY bytes, 0=empty, 255=exposed, row-wise from the
// top left.
func (file *File) DecodeLayer(index int) ([]uint8, error) {
	if index < 0 || index >= len(file.Layers) {
		return nil, fmt.Errorf("pwmx: layer %d out of range [0,%d)", index, len(file.Layers))
	}
	layer := file.Layers[index]
	start, end := int(layer.DataAddress), int(layer.DataAddress)+int(layer.DataLength)
	if start < 0 || end > len(file.raw) || start > end {
		return nil, fmt.Errorf("pwmx: layer %d data outside the file", index)
	}
	rle := file.raw[start:end]
	pixelCount := int(file.Header.ResolutionX) * int(file.Header.ResolutionY)
	output := make([]uint8, pixelCount)
	position := 0
	for byteIndex := 0; byteIndex+1 < len(rle); byteIndex += 2 {
		word := uint16(rle[byteIndex])<<8 | uint16(rle[byteIndex+1]) // big-endian
		grey := uint8((word>>12)&0xF) * 17                           // 0..15 → 0..255
		run := int(word & 0x0FFF)
		for repeat := 0; repeat < run && position < pixelCount; repeat++ {
			output[position] = grey
			position++
		}
	}
	if position != pixelCount {
		return output, fmt.Errorf("pwmx: layer %d: decoded %d pixels, expected %d", index, position, pixelCount)
	}
	return output, nil
}

func trimTag(data []byte) string {
	length := len(data)
	for length > 0 && (data[length-1] == 0) {
		length--
	}
	return string(data[:length])
}

func le32(data []byte, offset int) uint32 { return binary.LittleEndian.Uint32(data[offset : offset+4]) }

func f32(data []byte, offset int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
}
