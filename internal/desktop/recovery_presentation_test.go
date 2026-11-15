package desktop

import "testing"

func solidFrame(width, height int, red, green, blue, alpha byte) []byte {
	pixels := make([]byte, width*height*4)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = red, green, blue, alpha
	}
	return pixels
}

func paintRect(pixels []byte, width, x0, y0, x1, y1 int, red, green, blue, alpha byte) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			i := (y*width + x) * 4
			pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = red, green, blue, alpha
		}
	}
}

// A window can acknowledge the hidden probe and still show the user one
// colour. Faint rules and the top control band are not content.
func TestFlatPresentedWindowIsBlankBelowTheControls(t *testing.T) {
	const width, height = 80, 60
	flat := solidFrame(width, height, 249, 250, 251, 255)
	if !presentedFrameBlank(flat, width, height) {
		t.Fatal("uniform presented frame was accepted")
	}
	if presentationLuma(249, 250, 251)-presentationLuma(214, 217, 224) >= presentationContrast {
		t.Fatal("column-rule fixture is darker than the blank threshold")
	}
	rules := append([]byte(nil), flat...)
	for y := 0; y < height; y++ {
		paintRect(rules, width, 20, y, 21, y+1, 214, 217, 224, 255)
		paintRect(rules, width, 50, y, 51, y+1, 214, 217, 224, 255)
	}
	if !presentedFrameBlank(rules, width, height) {
		t.Fatal("faint column rules were treated as visible content")
	}
	chrome := append([]byte(nil), flat...)
	_, top, _, bottom := presentationCellBounds(width, height, 0, 0)
	paintRect(chrome, width, 0, top, width, bottom, 0, 0, 0, 255)
	if !presentedFrameBlank(chrome, width, height) {
		t.Fatal("top-band controls hid a flat frame")
	}
	content := append([]byte(nil), flat...)
	x0, y0, x1, y1 := presentationCellBounds(width, height, 3, 2)
	paintRect(content, width, x0, y0, x1, y1, 32, 36, 48, 255)
	if presentedFrameBlank(content, width, height) {
		t.Fatal("a contrasting region below the top band was discarded")
	}
	ink := append([]byte(nil), flat...)
	ix0, iy0, _, _ := presentationCellBounds(width, height, 4, 3)
	paintRect(ink, width, ix0, iy0, ix0+2, iy0+2, 32, 36, 48, 255)
	if presentedFrameBlank(ink, width, height) {
		t.Fatal("readable ink below the top band was discarded")
	}
	if presentedFrameBlank(flat[:len(flat)-1], width, height) || presentedFrameBlank(nil, width, height) {
		t.Fatal("incomplete frame was declared blank")
	}
	if presentedFrameBlank(solidFrame(8, 8, 255, 255, 255, 255), 8, 8) {
		t.Fatal("undersized frame was declared blank")
	}
	if presentedFrameBlank(solidFrame(width, height, 249, 250, 251, 0), width, height) {
		t.Fatal("transparent frame was declared blank")
	}
	sparse := solidFrame(width, height, 249, 250, 251, 255)
	sx0, sy0, sx1, sy1 := presentationCellBounds(width, height, 2, 2)
	paintRect(sparse, width, sx0, sy0, sx1, sy1, 0, 0, 0, 0)
	paintRect(sparse, width, sx0, sy0, sx0+1, sy0+2, 0, 0, 0, 255)
	if !presentedFrameBlank(sparse, width, height) {
		t.Fatal("a couple of opaque pixels counted as content")
	}
	specks := solidFrame(width, height, 249, 250, 251, 0)
	for cy := 1; cy < presentationGridY; cy++ {
		for cx := 0; cx < presentationGridX; cx++ {
			left, top, _, _ := presentationCellBounds(width, height, cx, cy)
			paintRect(specks, width, left, top, left+1, top+3, 32, 36, 48, 255)
		}
	}
	if presentedFrameBlank(specks, width, height) {
		t.Fatal("specks spread across cells were declared a blank frame")
	}
	if presentedSampleStatus(flat, width, height) != recoveryPresentationBlank {
		t.Fatal("flat sample lost the blank status")
	}
	if presentedSampleStatus(content, width, height) != 0 || presentedSampleStatus(flat, 5000, height) != 0 || presentedSampleStatus(flat[:3], width, height) != 0 {
		t.Fatal("readable or unusable sample was reported blank")
	}
}
