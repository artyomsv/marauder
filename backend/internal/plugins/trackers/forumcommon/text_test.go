package forumcommon

import (
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestCleanTitle_DecodesWindows1251AndStripsSuffix(t *testing.T) {
	cp1251, err := charmap.Windows1251.NewEncoder().String("Голод / Hunger :: RuTracker.org")
	if err != nil {
		t.Fatalf("encode cp1251: %v", err)
	}
	if got := CleanTitle(cp1251, " :: RuTracker.org"); got != "Голод / Hunger" {
		t.Errorf("CleanTitle = %q, want %q", got, "Голод / Hunger")
	}
}

func TestDecodeWindows1251_AlreadyUTF8_Unchanged(t *testing.T) {
	in := "Plain ASCII and УТФ-8 текст"
	if got := DecodeWindows1251(in); got != in {
		t.Errorf("DecodeWindows1251 = %q, want %q", got, in)
	}
}
