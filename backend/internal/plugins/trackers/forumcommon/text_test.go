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

func TestEncodeWindows1251Query(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"ascii passthrough", "dune", "dune"},
		{"space escaped", "dune part", "dune%20part"},
		// Д=0xC4 ю=0xFE н=0xED а=0xE0 in cp1251.
		{"cyrillic cp1251 bytes", "Дюна", "%C4%FE%ED%E0"},
		{"mixed", "Дюна 2", "%C4%FE%ED%E0%202"},
		{"unreserved kept", "a-b_c.d~e", "a-b_c.d~e"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeWindows1251Query(tt.in); got != tt.want {
				t.Errorf("EncodeWindows1251Query(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
