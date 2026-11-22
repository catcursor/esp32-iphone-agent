package daemon

import "github.com/onehumanbeing/esp32-iphone-agent/cli/internal/textloc"

// Screenshot carries one encoded frame and the text found in those exact bytes.
// SourceRect describes the area of the original frame represented by the image.
type Screenshot struct {
	Format       string         `json:"format"`
	Data         string         `json:"data,omitempty"`
	Device       string         `json:"device,omitempty"`
	FilePath     string         `json:"filePath,omitempty"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	SourceWidth  int            `json:"source_width,omitempty"`
	SourceHeight int            `json:"source_height,omitempty"`
	SourceRect   textloc.Box    `json:"source_rect,omitzero"`
	OCR          textloc.Result `json:"ocr"`
}
