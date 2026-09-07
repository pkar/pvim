package mode

import (
	"strings"

	"github.com/pkar/pvim/internal/key"
)

// cmdFormat renders typed keys the way the normal-mode tables spell them.
//
// It is key.Format with one difference, and the difference is the whole reason
// the function exists: an unmodified '<' prints as itself here and as "<lt>" in
// key.Format. Both are right for their own job. keytrans() prints "<lt>" so
// that ":map" output reads back through Parse unambiguously, and normal mode
// dispatches on characters, where '<' is the shift-left operator and nothing
// else. Formatting the notation and then switching on "<" is how "<<" quietly
// did nothing at all while ">>" worked, which the oracle caught and no unit
// test in this package would have, because the tests type keys through the same
// formatter.
//
// Only a bare '<' is unescaped. A modified one still prints as "<C-lt>",
// because there the angle brackets are structure rather than a character.
func cmdFormat(keys []key.Key) string {
	var b strings.Builder
	for _, k := range keys {
		if k.Special == key.KeyNone && k.Mod == 0 && k.Rune == '<' {
			b.WriteByte('<')
			continue
		}
		b.WriteString(k.String())
	}
	return b.String()
}
