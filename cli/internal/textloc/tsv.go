//go:build !windows

package textloc

import (
	"fmt"
	"image"
	"strconv"
	"strings"
)

func parseTSV(output string) ([][]word, error) {
	var lines [][]word
	var last string
	for row := range strings.SplitSeq(output, "\n") {
		fields := strings.SplitN(strings.TrimSuffix(row, "\r"), "\t", 12)
		if len(fields) == 0 || fields[0] != "5" { // word level
			continue
		}
		if len(fields) != 12 {
			return nil, fmt.Errorf("invalid Tesseract word row")
		}
		var box [4]int
		for i := range box {
			n, err := strconv.Atoi(fields[6+i])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid Tesseract word bounds")
			}
			box[i] = n
		}
		group := strings.Join(fields[1:5], "/") // page, block, paragraph, line
		if group != last {
			lines = append(lines, nil)
			last = group
		}
		w := word{Text: fields[11], Box: image.Rect(box[0], box[1], box[0]+box[2], box[1]+box[3])}
		if score, err := strconv.ParseFloat(fields[10], 64); err == nil && score >= 0 && score <= 100 {
			conf := score / 100
			w.Confidence = &conf
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], w)
	}
	return lines, nil
}
