// Package qr renders an invite link as an SVG, and nothing else.
//
// THE DECISION BEHIND THIS FILE (docs/decisions/2026-10-15-invite-qr-encoder.md): the encoder is
// github.com/skip2/go-qrcode, adopted for the MATRIX ONLY, because the hard and invisible part of a QR
// code is Reed-Solomon, the mask rules and the version tables -- and the part worth owning is the
// OUTPUT. The dependency has no SVG writer, so the SVG here is a projection of its bitmap: a text
// document, no image pipeline, no embedded binary, and no encoding of our own to get subtly wrong.
// The dependency is pinned with zero transitive dependencies, MEASURED with `go mod graph` rather
// than read off a README.
package qr

import (
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// QuietZone is the number of light modules the specification requires around the symbol (ISO/IEC
// 18004), and the one thing a matrix-only library leaves to its caller. A scanner that fails for a
// missing quiet zone is indistinguishable from a scanner that fails for a broken encoder, so the
// renderer asserts the margin is there rather than trusting the source of its matrix.
const QuietZone = 4

// SVG renders content as a QR code and returns it as a standalone SVG document.
//
// scale is the pixel size of one module in the document's width/height attributes; the geometry is
// expressed in MODULE units in the viewBox, so any consumer that scales the image gets crisp edges
// rather than a resampled bitmap.
func SVG(content string, scale int) (string, error) {
	if scale <= 0 {
		scale = 4
	}
	if content == "" {
		return "", fmt.Errorf("nothing to encode")
	}

	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", err
	}

	matrix := code.Bitmap()
	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return "", fmt.Errorf("the encoder produced an empty matrix")
	}
	if len(matrix) != len(matrix[0]) {
		return "", fmt.Errorf("the encoder produced a %dx%d matrix, which is not square",
			len(matrix), len(matrix[0]))
	}

	side := len(matrix)
	if err := assertQuietZone(matrix); err != nil {
		return "", err
	}

	var b strings.Builder
	// The viewBox is in module units and the width/height are in pixels: one coordinate system for the
	// geometry and one for the size, so a caller can set any scale without the geometry moving.
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
		`viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="%s">`,
		side*scale, side*scale, side, side, escapeAttr("invitation QR code"))

	// Light modules are the page's own background, so only the dark runs are drawn -- one <rect> per
	// RUN rather than per module, which is the difference between a few hundred elements and a few
	// thousand for the same picture.
	b.WriteString(`<rect width="` + itoa(side) + `" height="` + itoa(side) + `" fill="#ffffff"/>`)
	for y, row := range matrix {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			run := 1
			for x+run < len(row) && row[x+run] {
				run++
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="1" fill="#000000"/>`, x, y, run)
			x += run
		}
	}
	b.WriteString(`</svg>`)
	return b.String(), nil
}

// assertQuietZone checks the source matrix really carries the margin, so a change in the dependency
// (or a future switch to DisableBorder) fails here rather than at somebody's scanner.
func assertQuietZone(matrix [][]bool) error {
	side := len(matrix)
	for i := 0; i < QuietZone; i++ {
		for _, on := range matrix[i] {
			if on {
				return fmt.Errorf("row %d of the matrix is inside the quiet zone but has a dark module", i)
			}
		}
		for _, on := range matrix[side-1-i] {
			if on {
				return fmt.Errorf("row %d of the matrix is inside the quiet zone but has a dark module", side-1-i)
			}
		}
		for _, row := range matrix {
			if row[i] || row[side-1-i] {
				return fmt.Errorf("column %d of the matrix is inside the quiet zone but has a dark module", i)
			}
		}
	}
	return nil
}

// Bitmap exposes the matrix this package renders, for a test that wants to check the quiet zone and
// the round trip without going through the SVG.
func Bitmap(content string) ([][]bool, error) {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	return code.Bitmap(), nil
}

func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
