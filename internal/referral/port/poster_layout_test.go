package port

import "testing"

func TestPosterTemplateEnlargedCodeKeepsCenterAndLegacyDimensionFallback(t *testing.T) {
	layout := DefaultPosterQRLayout(1250, 2426)
	if layout != (PosterQRLayout{X: 810, Y: 1932, Size: 370}) || !layout.Fits(1250, 2426) {
		t.Fatalf("new template=%+v", layout)
	}
	if layout.X*2+layout.Size != 830*2+330 || layout.Y*2+layout.Size != 1952*2+330 {
		t.Fatal("code moved away from its existing center")
	}
	if legacy := DefaultPosterQRLayout(600, 800); legacy != (PosterQRLayout{X: 414, Y: 602, Size: 150}) {
		t.Fatalf("legacy geometry changed: %+v", legacy)
	}
}
