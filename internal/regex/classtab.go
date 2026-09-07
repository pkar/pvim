package regex

// The character-class bodies that are lists of code points rather than rules.
//
// \k, \p, [[:lower:]] and [[:upper:]] cannot be written down from the help.
// Vim answers each of them out of a table: 'iskeyword' and utf_class for the
// keyword characters, utf_printable's hole list for the printable ones, and
// utf_toupper for the two case classes, which asks whether the character has a
// counterpart and not which Unicode category it is in. All three disagree with
// the categories a translator would otherwise reach for, in both directions, so
// none of them is reasoned about here. They are read off vim 9.2 and pasted in.
//
// Regenerate after a brew upgrade moves vim by sweeping every code point
// through the binary the rest of this package is measured against, under
// `vim --clean -i NONE --not-a-term -es`:
//
//	while cp <= 0x10ffff
//	 let m = (nr2char(cp) =~# '^\k$')
//	 ...
//
// with the same expression for '^\p$', '^[[:lower:]]$' and '^[[:upper:]]$',
// collecting the runs that matched. TestClassesAgainstVim does that sweep over
// the BMP on every test run and fails when a table here has drifted from the
// binary, so a regeneration is never something anybody has to remember.
//
// The ranges stop at U+D7FF and pick up at U+E000 because vim calls the
// surrogates unprintable. Nothing else in the tables has an opinion about them:
// no well-formed UTF-8 can hold one, so they never reach a match either way.
const (
	// keywordBody is 'iskeyword' at its default below U+0100 and vim's
	// utf_class table above it. utf_class marks the punctuation and space
	// intervals it knows and calls everything else a word character, which is
	// far wider than any set of Unicode categories: an emoji is a keyword
	// character, a superscript zero is, the byte order mark is, and the two
	// ordinal indicators and the combining marks for symbols are not.
	keywordBody = `0-9A-Z_a-z\x{b5}\x{c0}-\x{37d}` +
		`\x{37f}-\x{386}\x{388}-\x{559}\x{560}-\x{588}\x{58a}-\x{5bd}\x{5bf}\x{5c1}-\x{5c2}` +
		`\x{5c4}-\x{5f2}\x{5f5}-\x{60b}\x{60d}-\x{61a}\x{61c}-\x{61e}\x{620}-\x{669}\x{66e}-\x{6d3}` +
		`\x{6d5}-\x{6ff}\x{70e}-\x{963}\x{966}-\x{96f}\x{971}-\x{df3}\x{df5}-\x{e4e}\x{e50}-\x{e59}` +
		`\x{e5c}-\x{f03}\x{f13}-\x{f39}\x{f3e}-\x{f84}\x{f86}-\x{1049}\x{1050}-\x{10fa}\x{10fc}-\x{1360}` +
		`\x{1369}-\x{166c}\x{166f}-\x{167f}\x{1681}-\x{169a}\x{169d}-\x{16ea}\x{16ee}-\x{1734}\x{1737}-\x{17d3}` +
		`\x{17dd}-\x{17ff}\x{180b}-\x{1fff}\x{203c}\x{2049}\x{2070}-\x{209f}\x{2122}` +
		`\x{2139}\x{2194}-\x{2199}\x{21a9}-\x{21aa}\x{231a}-\x{231b}\x{2328}\x{23cf}` +
		`\x{23e9}-\x{23f3}\x{23f8}-\x{23fa}\x{24c2}\x{25aa}-\x{25ab}\x{25b6}\x{25c0}` +
		`\x{25fb}-\x{25fe}\x{2600}-\x{2604}\x{260e}\x{2611}\x{2614}-\x{2615}\x{2618}` +
		`\x{261d}\x{2620}\x{2622}-\x{2623}\x{2626}\x{262a}\x{262e}-\x{262f}` +
		`\x{2638}-\x{263a}\x{2640}\x{2642}\x{2648}-\x{2653}\x{265f}-\x{2660}\x{2663}` +
		`\x{2665}-\x{2666}\x{2668}\x{267b}\x{267e}-\x{267f}\x{2692}-\x{2697}\x{2699}` +
		`\x{269b}-\x{269c}\x{26a0}-\x{26a1}\x{26a7}\x{26aa}-\x{26ab}\x{26b0}-\x{26b1}\x{26bd}-\x{26be}` +
		`\x{26c4}-\x{26c5}\x{26c8}\x{26ce}-\x{26cf}\x{26d1}\x{26d3}-\x{26d4}\x{26e9}-\x{26ea}` +
		`\x{26f0}-\x{26f5}\x{26f7}-\x{26fa}\x{26fd}\x{2702}\x{2705}\x{2708}-\x{270d}` +
		`\x{270f}\x{2712}\x{2714}\x{2716}\x{271d}\x{2721}` +
		`\x{2728}\x{2733}-\x{2734}\x{2744}\x{2747}\x{274c}\x{274e}` +
		`\x{2753}-\x{2755}\x{2757}\x{2763}-\x{2764}\x{2795}-\x{2797}\x{27a1}\x{27b0}` +
		`\x{27bf}\x{2800}-\x{28ff}\x{2934}-\x{2935}\x{2999}-\x{29d7}\x{29dc}-\x{29fb}\x{29fe}-\x{2dff}` +
		`\x{2e80}-\x{2fff}\x{3021}-\x{d7ff}\x{e000}-\x{fd3d}\x{fd40}-\x{fe2f}\x{fe6c}-\x{feff}\x{ff10}-\x{ff19}` +
		`\x{ff21}-\x{ff3a}\x{ff41}-\x{ff5a}\x{ff66}-\x{1cfff}\x{1d250}-\x{1d3ff}\x{1d800}-\x{1efff}\x{1f004}` +
		`\x{1f0cf}\x{1f170}-\x{1f171}\x{1f17e}-\x{1f17f}\x{1f18e}\x{1f191}-\x{1f19a}\x{1f1e6}-\x{1f1ff}` +
		`\x{1f201}-\x{1f202}\x{1f21a}\x{1f22f}\x{1f232}-\x{1f23a}\x{1f250}-\x{1f251}\x{1f300}-\x{1f321}` +
		`\x{1f324}-\x{1f393}\x{1f396}-\x{1f397}\x{1f399}-\x{1f39b}\x{1f39e}-\x{1f3f0}\x{1f3f3}-\x{1f3f5}\x{1f3f7}-\x{1f4fd}` +
		`\x{1f4ff}-\x{1f53d}\x{1f549}-\x{1f54e}\x{1f550}-\x{1f567}\x{1f56f}-\x{1f570}\x{1f573}-\x{1f57a}\x{1f587}` +
		`\x{1f58a}-\x{1f58d}\x{1f590}\x{1f595}-\x{1f596}\x{1f5a4}-\x{1f5a5}\x{1f5a8}\x{1f5b1}-\x{1f5b2}` +
		`\x{1f5bc}\x{1f5c2}-\x{1f5c4}\x{1f5d1}-\x{1f5d3}\x{1f5dc}-\x{1f5de}\x{1f5e1}\x{1f5e3}` +
		`\x{1f5e8}\x{1f5ef}\x{1f5f3}\x{1f5fa}-\x{1f64f}\x{1f680}-\x{1f6c5}\x{1f6cb}-\x{1f6d2}` +
		`\x{1f6d5}-\x{1f6d7}\x{1f6dc}-\x{1f6e5}\x{1f6e9}\x{1f6eb}-\x{1f6ec}\x{1f6f0}\x{1f6f3}-\x{1f6fc}` +
		`\x{1f7e0}-\x{1f7eb}\x{1f7f0}\x{1f90c}-\x{1f93a}\x{1f93c}-\x{1f945}\x{1f947}-\x{10ffff}`

	// keywordNoDigits is \K, which is \k without the ASCII digits and only
	// those: vim's SKWORD asks VIM_ISDIGIT, which has never heard of U+0663.
	keywordNoDigits = `A-Z_a-z\x{b5}\x{c0}-\x{37d}\x{37f}-\x{386}` +
		`\x{388}-\x{559}\x{560}-\x{588}\x{58a}-\x{5bd}\x{5bf}\x{5c1}-\x{5c2}\x{5c4}-\x{5f2}` +
		`\x{5f5}-\x{60b}\x{60d}-\x{61a}\x{61c}-\x{61e}\x{620}-\x{669}\x{66e}-\x{6d3}\x{6d5}-\x{6ff}` +
		`\x{70e}-\x{963}\x{966}-\x{96f}\x{971}-\x{df3}\x{df5}-\x{e4e}\x{e50}-\x{e59}\x{e5c}-\x{f03}` +
		`\x{f13}-\x{f39}\x{f3e}-\x{f84}\x{f86}-\x{1049}\x{1050}-\x{10fa}\x{10fc}-\x{1360}\x{1369}-\x{166c}` +
		`\x{166f}-\x{167f}\x{1681}-\x{169a}\x{169d}-\x{16ea}\x{16ee}-\x{1734}\x{1737}-\x{17d3}\x{17dd}-\x{17ff}` +
		`\x{180b}-\x{1fff}\x{203c}\x{2049}\x{2070}-\x{209f}\x{2122}\x{2139}` +
		`\x{2194}-\x{2199}\x{21a9}-\x{21aa}\x{231a}-\x{231b}\x{2328}\x{23cf}\x{23e9}-\x{23f3}` +
		`\x{23f8}-\x{23fa}\x{24c2}\x{25aa}-\x{25ab}\x{25b6}\x{25c0}\x{25fb}-\x{25fe}` +
		`\x{2600}-\x{2604}\x{260e}\x{2611}\x{2614}-\x{2615}\x{2618}\x{261d}` +
		`\x{2620}\x{2622}-\x{2623}\x{2626}\x{262a}\x{262e}-\x{262f}\x{2638}-\x{263a}` +
		`\x{2640}\x{2642}\x{2648}-\x{2653}\x{265f}-\x{2660}\x{2663}\x{2665}-\x{2666}` +
		`\x{2668}\x{267b}\x{267e}-\x{267f}\x{2692}-\x{2697}\x{2699}\x{269b}-\x{269c}` +
		`\x{26a0}-\x{26a1}\x{26a7}\x{26aa}-\x{26ab}\x{26b0}-\x{26b1}\x{26bd}-\x{26be}\x{26c4}-\x{26c5}` +
		`\x{26c8}\x{26ce}-\x{26cf}\x{26d1}\x{26d3}-\x{26d4}\x{26e9}-\x{26ea}\x{26f0}-\x{26f5}` +
		`\x{26f7}-\x{26fa}\x{26fd}\x{2702}\x{2705}\x{2708}-\x{270d}\x{270f}` +
		`\x{2712}\x{2714}\x{2716}\x{271d}\x{2721}\x{2728}` +
		`\x{2733}-\x{2734}\x{2744}\x{2747}\x{274c}\x{274e}\x{2753}-\x{2755}` +
		`\x{2757}\x{2763}-\x{2764}\x{2795}-\x{2797}\x{27a1}\x{27b0}\x{27bf}` +
		`\x{2800}-\x{28ff}\x{2934}-\x{2935}\x{2999}-\x{29d7}\x{29dc}-\x{29fb}\x{29fe}-\x{2dff}\x{2e80}-\x{2fff}` +
		`\x{3021}-\x{d7ff}\x{e000}-\x{fd3d}\x{fd40}-\x{fe2f}\x{fe6c}-\x{feff}\x{ff10}-\x{ff19}\x{ff21}-\x{ff3a}` +
		`\x{ff41}-\x{ff5a}\x{ff66}-\x{1cfff}\x{1d250}-\x{1d3ff}\x{1d800}-\x{1efff}\x{1f004}\x{1f0cf}` +
		`\x{1f170}-\x{1f171}\x{1f17e}-\x{1f17f}\x{1f18e}\x{1f191}-\x{1f19a}\x{1f1e6}-\x{1f1ff}\x{1f201}-\x{1f202}` +
		`\x{1f21a}\x{1f22f}\x{1f232}-\x{1f23a}\x{1f250}-\x{1f251}\x{1f300}-\x{1f321}\x{1f324}-\x{1f393}` +
		`\x{1f396}-\x{1f397}\x{1f399}-\x{1f39b}\x{1f39e}-\x{1f3f0}\x{1f3f3}-\x{1f3f5}\x{1f3f7}-\x{1f4fd}\x{1f4ff}-\x{1f53d}` +
		`\x{1f549}-\x{1f54e}\x{1f550}-\x{1f567}\x{1f56f}-\x{1f570}\x{1f573}-\x{1f57a}\x{1f587}\x{1f58a}-\x{1f58d}` +
		`\x{1f590}\x{1f595}-\x{1f596}\x{1f5a4}-\x{1f5a5}\x{1f5a8}\x{1f5b1}-\x{1f5b2}\x{1f5bc}` +
		`\x{1f5c2}-\x{1f5c4}\x{1f5d1}-\x{1f5d3}\x{1f5dc}-\x{1f5de}\x{1f5e1}\x{1f5e3}\x{1f5e8}` +
		`\x{1f5ef}\x{1f5f3}\x{1f5fa}-\x{1f64f}\x{1f680}-\x{1f6c5}\x{1f6cb}-\x{1f6d2}\x{1f6d5}-\x{1f6d7}` +
		`\x{1f6dc}-\x{1f6e5}\x{1f6e9}\x{1f6eb}-\x{1f6ec}\x{1f6f0}\x{1f6f3}-\x{1f6fc}\x{1f7e0}-\x{1f7eb}` +
		`\x{1f7f0}\x{1f90c}-\x{1f93a}\x{1f93c}-\x{1f945}\x{1f947}-\x{10ffff}`

	// printBody is 'isprint', which is printable ASCII plus everything from
	// U+00A0 up except the holes in vim's utf_printable: the Syriac
	// abbreviation mark, the Mongolian and zero-width format characters, the
	// bidi overrides, the word joiner block, the byte order mark and the
	// non-characters at the end of the BMP.
	printBody = `\x{20}-\x{7e}\x{a0}-\x{70e}\x{710}-\x{180a}\x{180f}-\x{200a}\x{2010}-\x{2029}\x{202f}-\x{205f}` +
		`\x{2070}-\x{d7ff}\x{e000}-\x{fefe}\x{ff00}-\x{fff8}\x{fffc}-\x{fffd}\x{10000}-\x{10ffff}`

	// printNoDigits is \P, print without the ASCII digits.
	printNoDigits = `\x{20}-\x{2f}\x{3a}-\x{7e}\x{a0}-\x{70e}\x{710}-\x{180a}\x{180f}-\x{200a}\x{2010}-\x{2029}` +
		`\x{202f}-\x{205f}\x{2070}-\x{d7ff}\x{e000}-\x{fefe}\x{ff00}-\x{fff8}\x{fffc}-\x{fffd}\x{10000}-\x{10ffff}`

	// lowerBody is [[:lower:]], which in vim is utf_islower: the character has
	// an upper case counterpart, plus the sharp s, which has none and is lower
	// case anyway. That is not \p{Ll}. Kra and the IPA letters with no upper
	// case are Ll and are not lower here; the titlecase letters and the small
	// roman numerals are not Ll and are.
	lowerBody = `a-z\x{b5}\x{df}-\x{f6}\x{f8}-\x{ff}\x{101}\x{103}` +
		`\x{105}\x{107}\x{109}\x{10b}\x{10d}\x{10f}` +
		`\x{111}\x{113}\x{115}\x{117}\x{119}\x{11b}` +
		`\x{11d}\x{11f}\x{121}\x{123}\x{125}\x{127}` +
		`\x{129}\x{12b}\x{12d}\x{12f}\x{131}\x{133}` +
		`\x{135}\x{137}\x{13a}\x{13c}\x{13e}\x{140}` +
		`\x{142}\x{144}\x{146}\x{148}\x{14b}\x{14d}` +
		`\x{14f}\x{151}\x{153}\x{155}\x{157}\x{159}` +
		`\x{15b}\x{15d}\x{15f}\x{161}\x{163}\x{165}` +
		`\x{167}\x{169}\x{16b}\x{16d}\x{16f}\x{171}` +
		`\x{173}\x{175}\x{177}\x{17a}\x{17c}\x{17e}-\x{180}` +
		`\x{183}\x{185}\x{188}\x{18c}\x{192}\x{195}` +
		`\x{199}-\x{19b}\x{19e}\x{1a1}\x{1a3}\x{1a5}\x{1a8}` +
		`\x{1ad}\x{1b0}\x{1b4}\x{1b6}\x{1b9}\x{1bd}` +
		`\x{1bf}\x{1c5}-\x{1c6}\x{1c8}-\x{1c9}\x{1cb}-\x{1cc}\x{1ce}\x{1d0}` +
		`\x{1d2}\x{1d4}\x{1d6}\x{1d8}\x{1da}\x{1dc}-\x{1dd}` +
		`\x{1df}\x{1e1}\x{1e3}\x{1e5}\x{1e7}\x{1e9}` +
		`\x{1eb}\x{1ed}\x{1ef}\x{1f2}-\x{1f3}\x{1f5}\x{1f9}` +
		`\x{1fb}\x{1fd}\x{1ff}\x{201}\x{203}\x{205}` +
		`\x{207}\x{209}\x{20b}\x{20d}\x{20f}\x{211}` +
		`\x{213}\x{215}\x{217}\x{219}\x{21b}\x{21d}` +
		`\x{21f}\x{223}\x{225}\x{227}\x{229}\x{22b}` +
		`\x{22d}\x{22f}\x{231}\x{233}\x{23c}\x{23f}-\x{240}` +
		`\x{242}\x{247}\x{249}\x{24b}\x{24d}\x{24f}-\x{254}` +
		`\x{256}-\x{257}\x{259}\x{25b}-\x{25c}\x{260}-\x{261}\x{263}-\x{266}\x{268}-\x{26c}` +
		`\x{26f}\x{271}-\x{272}\x{275}\x{27d}\x{280}\x{282}-\x{283}` +
		`\x{287}-\x{28c}\x{292}\x{29d}-\x{29e}\x{345}\x{371}\x{373}` +
		`\x{377}\x{37b}-\x{37d}\x{3ac}-\x{3af}\x{3b1}-\x{3ce}\x{3d0}-\x{3d1}\x{3d5}-\x{3d7}` +
		`\x{3d9}\x{3db}\x{3dd}\x{3df}\x{3e1}\x{3e3}` +
		`\x{3e5}\x{3e7}\x{3e9}\x{3eb}\x{3ed}\x{3ef}-\x{3f3}` +
		`\x{3f5}\x{3f8}\x{3fb}\x{430}-\x{45f}\x{461}\x{463}` +
		`\x{465}\x{467}\x{469}\x{46b}\x{46d}\x{46f}` +
		`\x{471}\x{473}\x{475}\x{477}\x{479}\x{47b}` +
		`\x{47d}\x{47f}\x{481}\x{48b}\x{48d}\x{48f}` +
		`\x{491}\x{493}\x{495}\x{497}\x{499}\x{49b}` +
		`\x{49d}\x{49f}\x{4a1}\x{4a3}\x{4a5}\x{4a7}` +
		`\x{4a9}\x{4ab}\x{4ad}\x{4af}\x{4b1}\x{4b3}` +
		`\x{4b5}\x{4b7}\x{4b9}\x{4bb}\x{4bd}\x{4bf}` +
		`\x{4c2}\x{4c4}\x{4c6}\x{4c8}\x{4ca}\x{4cc}` +
		`\x{4ce}-\x{4cf}\x{4d1}\x{4d3}\x{4d5}\x{4d7}\x{4d9}` +
		`\x{4db}\x{4dd}\x{4df}\x{4e1}\x{4e3}\x{4e5}` +
		`\x{4e7}\x{4e9}\x{4eb}\x{4ed}\x{4ef}\x{4f1}` +
		`\x{4f3}\x{4f5}\x{4f7}\x{4f9}\x{4fb}\x{4fd}` +
		`\x{4ff}\x{501}\x{503}\x{505}\x{507}\x{509}` +
		`\x{50b}\x{50d}\x{50f}\x{511}\x{513}\x{515}` +
		`\x{517}\x{519}\x{51b}\x{51d}\x{51f}\x{521}` +
		`\x{523}\x{525}\x{527}\x{529}\x{52b}\x{52d}` +
		`\x{52f}\x{561}-\x{586}\x{10d0}-\x{10fa}\x{10fd}-\x{10ff}\x{13f8}-\x{13fd}\x{1c80}-\x{1c88}` +
		`\x{1c8a}\x{1d79}\x{1d7d}\x{1d8e}\x{1e01}\x{1e03}` +
		`\x{1e05}\x{1e07}\x{1e09}\x{1e0b}\x{1e0d}\x{1e0f}` +
		`\x{1e11}\x{1e13}\x{1e15}\x{1e17}\x{1e19}\x{1e1b}` +
		`\x{1e1d}\x{1e1f}\x{1e21}\x{1e23}\x{1e25}\x{1e27}` +
		`\x{1e29}\x{1e2b}\x{1e2d}\x{1e2f}\x{1e31}\x{1e33}` +
		`\x{1e35}\x{1e37}\x{1e39}\x{1e3b}\x{1e3d}\x{1e3f}` +
		`\x{1e41}\x{1e43}\x{1e45}\x{1e47}\x{1e49}\x{1e4b}` +
		`\x{1e4d}\x{1e4f}\x{1e51}\x{1e53}\x{1e55}\x{1e57}` +
		`\x{1e59}\x{1e5b}\x{1e5d}\x{1e5f}\x{1e61}\x{1e63}` +
		`\x{1e65}\x{1e67}\x{1e69}\x{1e6b}\x{1e6d}\x{1e6f}` +
		`\x{1e71}\x{1e73}\x{1e75}\x{1e77}\x{1e79}\x{1e7b}` +
		`\x{1e7d}\x{1e7f}\x{1e81}\x{1e83}\x{1e85}\x{1e87}` +
		`\x{1e89}\x{1e8b}\x{1e8d}\x{1e8f}\x{1e91}\x{1e93}` +
		`\x{1e95}\x{1e9b}\x{1ea1}\x{1ea3}\x{1ea5}\x{1ea7}` +
		`\x{1ea9}\x{1eab}\x{1ead}\x{1eaf}\x{1eb1}\x{1eb3}` +
		`\x{1eb5}\x{1eb7}\x{1eb9}\x{1ebb}\x{1ebd}\x{1ebf}` +
		`\x{1ec1}\x{1ec3}\x{1ec5}\x{1ec7}\x{1ec9}\x{1ecb}` +
		`\x{1ecd}\x{1ecf}\x{1ed1}\x{1ed3}\x{1ed5}\x{1ed7}` +
		`\x{1ed9}\x{1edb}\x{1edd}\x{1edf}\x{1ee1}\x{1ee3}` +
		`\x{1ee5}\x{1ee7}\x{1ee9}\x{1eeb}\x{1eed}\x{1eef}` +
		`\x{1ef1}\x{1ef3}\x{1ef5}\x{1ef7}\x{1ef9}\x{1efb}` +
		`\x{1efd}\x{1eff}-\x{1f07}\x{1f10}-\x{1f15}\x{1f20}-\x{1f27}\x{1f30}-\x{1f37}\x{1f40}-\x{1f45}` +
		`\x{1f51}\x{1f53}\x{1f55}\x{1f57}\x{1f60}-\x{1f67}\x{1f70}-\x{1f7d}` +
		`\x{1f80}-\x{1f87}\x{1f90}-\x{1f97}\x{1fa0}-\x{1fa7}\x{1fb0}-\x{1fb1}\x{1fb3}\x{1fbe}` +
		`\x{1fc3}\x{1fd0}-\x{1fd1}\x{1fe0}-\x{1fe1}\x{1fe5}\x{1ff3}\x{214e}` +
		`\x{2170}-\x{217f}\x{2184}\x{24d0}-\x{24e9}\x{2c30}-\x{2c5f}\x{2c61}\x{2c65}-\x{2c66}` +
		`\x{2c68}\x{2c6a}\x{2c6c}\x{2c73}\x{2c76}\x{2c81}` +
		`\x{2c83}\x{2c85}\x{2c87}\x{2c89}\x{2c8b}\x{2c8d}` +
		`\x{2c8f}\x{2c91}\x{2c93}\x{2c95}\x{2c97}\x{2c99}` +
		`\x{2c9b}\x{2c9d}\x{2c9f}\x{2ca1}\x{2ca3}\x{2ca5}` +
		`\x{2ca7}\x{2ca9}\x{2cab}\x{2cad}\x{2caf}\x{2cb1}` +
		`\x{2cb3}\x{2cb5}\x{2cb7}\x{2cb9}\x{2cbb}\x{2cbd}` +
		`\x{2cbf}\x{2cc1}\x{2cc3}\x{2cc5}\x{2cc7}\x{2cc9}` +
		`\x{2ccb}\x{2ccd}\x{2ccf}\x{2cd1}\x{2cd3}\x{2cd5}` +
		`\x{2cd7}\x{2cd9}\x{2cdb}\x{2cdd}\x{2cdf}\x{2ce1}` +
		`\x{2ce3}\x{2cec}\x{2cee}\x{2cf3}\x{2d00}-\x{2d25}\x{2d27}` +
		`\x{2d2d}\x{a641}\x{a643}\x{a645}\x{a647}\x{a649}` +
		`\x{a64b}\x{a64d}\x{a64f}\x{a651}\x{a653}\x{a655}` +
		`\x{a657}\x{a659}\x{a65b}\x{a65d}\x{a65f}\x{a661}` +
		`\x{a663}\x{a665}\x{a667}\x{a669}\x{a66b}\x{a66d}` +
		`\x{a681}\x{a683}\x{a685}\x{a687}\x{a689}\x{a68b}` +
		`\x{a68d}\x{a68f}\x{a691}\x{a693}\x{a695}\x{a697}` +
		`\x{a699}\x{a69b}\x{a723}\x{a725}\x{a727}\x{a729}` +
		`\x{a72b}\x{a72d}\x{a72f}\x{a733}\x{a735}\x{a737}` +
		`\x{a739}\x{a73b}\x{a73d}\x{a73f}\x{a741}\x{a743}` +
		`\x{a745}\x{a747}\x{a749}\x{a74b}\x{a74d}\x{a74f}` +
		`\x{a751}\x{a753}\x{a755}\x{a757}\x{a759}\x{a75b}` +
		`\x{a75d}\x{a75f}\x{a761}\x{a763}\x{a765}\x{a767}` +
		`\x{a769}\x{a76b}\x{a76d}\x{a76f}\x{a77a}\x{a77c}` +
		`\x{a77f}\x{a781}\x{a783}\x{a785}\x{a787}\x{a78c}` +
		`\x{a791}\x{a793}-\x{a794}\x{a797}\x{a799}\x{a79b}\x{a79d}` +
		`\x{a79f}\x{a7a1}\x{a7a3}\x{a7a5}\x{a7a7}\x{a7a9}` +
		`\x{a7b5}\x{a7b7}\x{a7b9}\x{a7bb}\x{a7bd}\x{a7bf}` +
		`\x{a7c1}\x{a7c3}\x{a7c8}\x{a7ca}\x{a7cd}\x{a7d1}` +
		`\x{a7d7}\x{a7d9}\x{a7db}\x{a7f6}\x{ab53}\x{ab70}-\x{abbf}` +
		`\x{ff41}-\x{ff5a}\x{10428}-\x{1044f}\x{104d8}-\x{104fb}\x{10597}-\x{105a1}\x{105a3}-\x{105b1}\x{105b3}-\x{105b9}` +
		`\x{105bb}-\x{105bc}\x{10cc0}-\x{10cf2}\x{10d70}-\x{10d85}\x{118c0}-\x{118df}\x{16e60}-\x{16e7f}\x{1e922}-\x{1e943}`

	// upperBody is [[:upper:]], utf_isupper: the character has a lower case
	// counterpart. Upsilon with a hook is Lu and has none, so it is not upper.
	upperBody = `A-Z\x{c0}-\x{d6}\x{d8}-\x{de}\x{100}\x{102}\x{104}` +
		`\x{106}\x{108}\x{10a}\x{10c}\x{10e}\x{110}` +
		`\x{112}\x{114}\x{116}\x{118}\x{11a}\x{11c}` +
		`\x{11e}\x{120}\x{122}\x{124}\x{126}\x{128}` +
		`\x{12a}\x{12c}\x{12e}\x{130}\x{132}\x{134}` +
		`\x{136}\x{139}\x{13b}\x{13d}\x{13f}\x{141}` +
		`\x{143}\x{145}\x{147}\x{14a}\x{14c}\x{14e}` +
		`\x{150}\x{152}\x{154}\x{156}\x{158}\x{15a}` +
		`\x{15c}\x{15e}\x{160}\x{162}\x{164}\x{166}` +
		`\x{168}\x{16a}\x{16c}\x{16e}\x{170}\x{172}` +
		`\x{174}\x{176}\x{178}-\x{179}\x{17b}\x{17d}\x{181}-\x{182}` +
		`\x{184}\x{186}-\x{187}\x{189}-\x{18b}\x{18e}-\x{191}\x{193}-\x{194}\x{196}-\x{198}` +
		`\x{19c}-\x{19d}\x{19f}-\x{1a0}\x{1a2}\x{1a4}\x{1a6}-\x{1a7}\x{1a9}` +
		`\x{1ac}\x{1ae}-\x{1af}\x{1b1}-\x{1b3}\x{1b5}\x{1b7}-\x{1b8}\x{1bc}` +
		`\x{1c4}-\x{1c5}\x{1c7}-\x{1c8}\x{1ca}-\x{1cb}\x{1cd}\x{1cf}\x{1d1}` +
		`\x{1d3}\x{1d5}\x{1d7}\x{1d9}\x{1db}\x{1de}` +
		`\x{1e0}\x{1e2}\x{1e4}\x{1e6}\x{1e8}\x{1ea}` +
		`\x{1ec}\x{1ee}\x{1f1}-\x{1f2}\x{1f4}\x{1f6}-\x{1f8}\x{1fa}` +
		`\x{1fc}\x{1fe}\x{200}\x{202}\x{204}\x{206}` +
		`\x{208}\x{20a}\x{20c}\x{20e}\x{210}\x{212}` +
		`\x{214}\x{216}\x{218}\x{21a}\x{21c}\x{21e}` +
		`\x{220}\x{222}\x{224}\x{226}\x{228}\x{22a}` +
		`\x{22c}\x{22e}\x{230}\x{232}\x{23a}-\x{23b}\x{23d}-\x{23e}` +
		`\x{241}\x{243}-\x{246}\x{248}\x{24a}\x{24c}\x{24e}` +
		`\x{370}\x{372}\x{376}\x{37f}\x{386}\x{388}-\x{38a}` +
		`\x{38c}\x{38e}-\x{38f}\x{391}-\x{3a1}\x{3a3}-\x{3ab}\x{3cf}\x{3d8}` +
		`\x{3da}\x{3dc}\x{3de}\x{3e0}\x{3e2}\x{3e4}` +
		`\x{3e6}\x{3e8}\x{3ea}\x{3ec}\x{3ee}\x{3f4}` +
		`\x{3f7}\x{3f9}-\x{3fa}\x{3fd}-\x{42f}\x{460}\x{462}\x{464}` +
		`\x{466}\x{468}\x{46a}\x{46c}\x{46e}\x{470}` +
		`\x{472}\x{474}\x{476}\x{478}\x{47a}\x{47c}` +
		`\x{47e}\x{480}\x{48a}\x{48c}\x{48e}\x{490}` +
		`\x{492}\x{494}\x{496}\x{498}\x{49a}\x{49c}` +
		`\x{49e}\x{4a0}\x{4a2}\x{4a4}\x{4a6}\x{4a8}` +
		`\x{4aa}\x{4ac}\x{4ae}\x{4b0}\x{4b2}\x{4b4}` +
		`\x{4b6}\x{4b8}\x{4ba}\x{4bc}\x{4be}\x{4c0}-\x{4c1}` +
		`\x{4c3}\x{4c5}\x{4c7}\x{4c9}\x{4cb}\x{4cd}` +
		`\x{4d0}\x{4d2}\x{4d4}\x{4d6}\x{4d8}\x{4da}` +
		`\x{4dc}\x{4de}\x{4e0}\x{4e2}\x{4e4}\x{4e6}` +
		`\x{4e8}\x{4ea}\x{4ec}\x{4ee}\x{4f0}\x{4f2}` +
		`\x{4f4}\x{4f6}\x{4f8}\x{4fa}\x{4fc}\x{4fe}` +
		`\x{500}\x{502}\x{504}\x{506}\x{508}\x{50a}` +
		`\x{50c}\x{50e}\x{510}\x{512}\x{514}\x{516}` +
		`\x{518}\x{51a}\x{51c}\x{51e}\x{520}\x{522}` +
		`\x{524}\x{526}\x{528}\x{52a}\x{52c}\x{52e}` +
		`\x{531}-\x{556}\x{10a0}-\x{10c5}\x{10c7}\x{10cd}\x{13a0}-\x{13f5}\x{1c89}` +
		`\x{1c90}-\x{1cba}\x{1cbd}-\x{1cbf}\x{1e00}\x{1e02}\x{1e04}\x{1e06}` +
		`\x{1e08}\x{1e0a}\x{1e0c}\x{1e0e}\x{1e10}\x{1e12}` +
		`\x{1e14}\x{1e16}\x{1e18}\x{1e1a}\x{1e1c}\x{1e1e}` +
		`\x{1e20}\x{1e22}\x{1e24}\x{1e26}\x{1e28}\x{1e2a}` +
		`\x{1e2c}\x{1e2e}\x{1e30}\x{1e32}\x{1e34}\x{1e36}` +
		`\x{1e38}\x{1e3a}\x{1e3c}\x{1e3e}\x{1e40}\x{1e42}` +
		`\x{1e44}\x{1e46}\x{1e48}\x{1e4a}\x{1e4c}\x{1e4e}` +
		`\x{1e50}\x{1e52}\x{1e54}\x{1e56}\x{1e58}\x{1e5a}` +
		`\x{1e5c}\x{1e5e}\x{1e60}\x{1e62}\x{1e64}\x{1e66}` +
		`\x{1e68}\x{1e6a}\x{1e6c}\x{1e6e}\x{1e70}\x{1e72}` +
		`\x{1e74}\x{1e76}\x{1e78}\x{1e7a}\x{1e7c}\x{1e7e}` +
		`\x{1e80}\x{1e82}\x{1e84}\x{1e86}\x{1e88}\x{1e8a}` +
		`\x{1e8c}\x{1e8e}\x{1e90}\x{1e92}\x{1e94}\x{1e9e}` +
		`\x{1ea0}\x{1ea2}\x{1ea4}\x{1ea6}\x{1ea8}\x{1eaa}` +
		`\x{1eac}\x{1eae}\x{1eb0}\x{1eb2}\x{1eb4}\x{1eb6}` +
		`\x{1eb8}\x{1eba}\x{1ebc}\x{1ebe}\x{1ec0}\x{1ec2}` +
		`\x{1ec4}\x{1ec6}\x{1ec8}\x{1eca}\x{1ecc}\x{1ece}` +
		`\x{1ed0}\x{1ed2}\x{1ed4}\x{1ed6}\x{1ed8}\x{1eda}` +
		`\x{1edc}\x{1ede}\x{1ee0}\x{1ee2}\x{1ee4}\x{1ee6}` +
		`\x{1ee8}\x{1eea}\x{1eec}\x{1eee}\x{1ef0}\x{1ef2}` +
		`\x{1ef4}\x{1ef6}\x{1ef8}\x{1efa}\x{1efc}\x{1efe}` +
		`\x{1f08}-\x{1f0f}\x{1f18}-\x{1f1d}\x{1f28}-\x{1f2f}\x{1f38}-\x{1f3f}\x{1f48}-\x{1f4d}\x{1f59}` +
		`\x{1f5b}\x{1f5d}\x{1f5f}\x{1f68}-\x{1f6f}\x{1f88}-\x{1f8f}\x{1f98}-\x{1f9f}` +
		`\x{1fa8}-\x{1faf}\x{1fb8}-\x{1fbc}\x{1fc8}-\x{1fcc}\x{1fd8}-\x{1fdb}\x{1fe8}-\x{1fec}\x{1ff8}-\x{1ffc}` +
		`\x{2126}\x{212a}-\x{212b}\x{2132}\x{2160}-\x{216f}\x{2183}\x{24b6}-\x{24cf}` +
		`\x{2c00}-\x{2c2f}\x{2c60}\x{2c62}-\x{2c64}\x{2c67}\x{2c69}\x{2c6b}` +
		`\x{2c6d}-\x{2c70}\x{2c72}\x{2c75}\x{2c7e}-\x{2c80}\x{2c82}\x{2c84}` +
		`\x{2c86}\x{2c88}\x{2c8a}\x{2c8c}\x{2c8e}\x{2c90}` +
		`\x{2c92}\x{2c94}\x{2c96}\x{2c98}\x{2c9a}\x{2c9c}` +
		`\x{2c9e}\x{2ca0}\x{2ca2}\x{2ca4}\x{2ca6}\x{2ca8}` +
		`\x{2caa}\x{2cac}\x{2cae}\x{2cb0}\x{2cb2}\x{2cb4}` +
		`\x{2cb6}\x{2cb8}\x{2cba}\x{2cbc}\x{2cbe}\x{2cc0}` +
		`\x{2cc2}\x{2cc4}\x{2cc6}\x{2cc8}\x{2cca}\x{2ccc}` +
		`\x{2cce}\x{2cd0}\x{2cd2}\x{2cd4}\x{2cd6}\x{2cd8}` +
		`\x{2cda}\x{2cdc}\x{2cde}\x{2ce0}\x{2ce2}\x{2ceb}` +
		`\x{2ced}\x{2cf2}\x{a640}\x{a642}\x{a644}\x{a646}` +
		`\x{a648}\x{a64a}\x{a64c}\x{a64e}\x{a650}\x{a652}` +
		`\x{a654}\x{a656}\x{a658}\x{a65a}\x{a65c}\x{a65e}` +
		`\x{a660}\x{a662}\x{a664}\x{a666}\x{a668}\x{a66a}` +
		`\x{a66c}\x{a680}\x{a682}\x{a684}\x{a686}\x{a688}` +
		`\x{a68a}\x{a68c}\x{a68e}\x{a690}\x{a692}\x{a694}` +
		`\x{a696}\x{a698}\x{a69a}\x{a722}\x{a724}\x{a726}` +
		`\x{a728}\x{a72a}\x{a72c}\x{a72e}\x{a732}\x{a734}` +
		`\x{a736}\x{a738}\x{a73a}\x{a73c}\x{a73e}\x{a740}` +
		`\x{a742}\x{a744}\x{a746}\x{a748}\x{a74a}\x{a74c}` +
		`\x{a74e}\x{a750}\x{a752}\x{a754}\x{a756}\x{a758}` +
		`\x{a75a}\x{a75c}\x{a75e}\x{a760}\x{a762}\x{a764}` +
		`\x{a766}\x{a768}\x{a76a}\x{a76c}\x{a76e}\x{a779}` +
		`\x{a77b}\x{a77d}-\x{a77e}\x{a780}\x{a782}\x{a784}\x{a786}` +
		`\x{a78b}\x{a78d}\x{a790}\x{a792}\x{a796}\x{a798}` +
		`\x{a79a}\x{a79c}\x{a79e}\x{a7a0}\x{a7a2}\x{a7a4}` +
		`\x{a7a6}\x{a7a8}\x{a7aa}-\x{a7ae}\x{a7b0}-\x{a7b4}\x{a7b6}\x{a7b8}` +
		`\x{a7ba}\x{a7bc}\x{a7be}\x{a7c0}\x{a7c2}\x{a7c4}-\x{a7c7}` +
		`\x{a7c9}\x{a7cb}-\x{a7cc}\x{a7d0}\x{a7d6}\x{a7d8}\x{a7da}` +
		`\x{a7dc}\x{a7f5}\x{ff21}-\x{ff3a}\x{10400}-\x{10427}\x{104b0}-\x{104d3}\x{10570}-\x{1057a}` +
		`\x{1057c}-\x{1058a}\x{1058c}-\x{10592}\x{10594}-\x{10595}\x{10c80}-\x{10cb2}\x{10d50}-\x{10d65}\x{118a0}-\x{118bf}` +
		`\x{16e40}-\x{16e5f}\x{1e900}-\x{1e921}`
)
