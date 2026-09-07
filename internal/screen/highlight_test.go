package screen

import "testing"

// Normal is nofrils-dark's Normal, read out of the colourscheme file the vimrc
// actually loads:
//
//	hi Normal ... gui=NONE guifg=#eeeeee guibg=#262626
//
// #bbbbbb is the foreground it is easy to assume. The file says #eeeeee, and
// the file is what MacVim renders, so the file wins.
func TestDefaultNormalIsNofrilsDark(t *testing.T) {
	if got := DefaultNormal.FG; got != (RGB{0xee, 0xee, 0xee}) {
		t.Errorf("Normal guifg = %+v, want #eeeeee", got)
	}
	if got := DefaultNormal.BG; got != (RGB{0x26, 0x26, 0x26}) {
		t.Errorf("Normal guibg = %+v, want #262626", got)
	}
	if got := DefaultNormal.Attr; got != 0 {
		t.Errorf("Normal attr = %d, want none; the scheme says gui=NONE", got)
	}
}

// Naming a group interns an id, and naming it again gives the same one. The
// vimrc parser asks by name once per `hi` line and the render path asks by id
// once per cell, and the two have to agree.
func TestTableIDsAreStable(t *testing.T) {
	tab := NewTable()

	search := tab.ID("Search")
	comment := tab.ID("Comment")

	if search == Normal || comment == Normal || search == comment {
		t.Fatalf("ids collided: Search=%d Comment=%d Normal=%d", search, comment, Normal)
	}
	if again := tab.ID("Search"); again != search {
		t.Errorf("ID(Search) = %d then %d, want the same id", search, again)
	}
	if got := tab.ID(NormalName); got != Normal {
		t.Errorf("ID(Normal) = %d, want %d", got, Normal)
	}
}

// A group named before it is defined renders as ordinary text. `hi link Foo
// Bar` can arrive in either order and a group nobody ever defines must not come
// out black on black.
func TestUndefinedGroupLooksLikeNormal(t *testing.T) {
	tab := NewTable()

	id := tab.ID("NeverDefined")

	if got := tab.Look(id); got != DefaultNormal {
		t.Errorf("Look(undefined) = %+v, want Normal %+v", got, DefaultNormal)
	}
}

// Set defines a group and Look reads it back, including the attribute bits the
// colourscheme's `gui=` column carries.
func TestTableSetAndLook(t *testing.T) {
	tab := NewTable()
	visual := Highlight{Attr: AttrReverse}
	spell := Highlight{FG: RGB{0xff, 0x00, 0xff}, Attr: AttrUnderline}

	vid := tab.Set("Visual", visual)
	sid := tab.Set("SpellBad", spell)

	if got := tab.Look(vid); got != visual {
		t.Errorf("Look(Visual) = %+v, want %+v", got, visual)
	}
	if got := tab.Look(sid); got != spell {
		t.Errorf("Look(SpellBad) = %+v, want %+v", got, spell)
	}
}

// Look has to survive an id the table does not have. A colourscheme reload
// shrinks the table while a frame is already on its way to a frontend, and a
// panic there is a crash in the paint path over a cosmetic change.
func TestLookFallsBackToNormal(t *testing.T) {
	tab := NewTable()
	tab.Set("Comment", Highlight{FG: RGB{0x6c, 0x6c, 0x6c}})

	for _, id := range []HLID{2, 99, -1} {
		if got := tab.Look(id); got != DefaultNormal {
			t.Errorf("Look(%d) = %+v, want the Normal fallback", id, got)
		}
	}
	var nilTable *Table
	if got := nilTable.Look(3); got != DefaultNormal {
		t.Errorf("a nil table's Look = %+v, want Normal", got)
	}
}

// Names is what the dump legend reads. It has to come back in id order, or a
// screendump names the wrong group and the test that uses it lies.
func TestTableNamesAreInIDOrder(t *testing.T) {
	tab := NewTable()
	tab.Set("Search", Highlight{})
	tab.Set("Comment", Highlight{})

	got := tab.Names()
	want := []string{NormalName, "Search", "Comment"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
