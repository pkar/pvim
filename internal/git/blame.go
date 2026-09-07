package git

import (
	"strings"
	"time"
)

// Blame: ":Gblame" is a left split with one line per line of the file, scroll
// locked to it, and <CR> on a line opens the commit that line came from.

// BlameLine is one line's worth of blame.
type BlameLine struct {
	// Hash is the full commit id. Abbrev is what the buffer shows.
	Hash   string
	Abbrev string
	// Boundary is a commit at the edge of the history that was walked, which
	// git marks with a "^" in front of the abbreviated id.
	Boundary bool
	// Author, When and Zone are the authorship, not the committership: that
	// is what `git blame` shows and what a person is asking for when they
	// ask who wrote a line.
	Author string
	When   time.Time
	Zone   string
	// Summary is the commit's subject line, which is not on a `git blame`
	// line and is what this editor puts on the message line when the cursor
	// moves, because the subject is the thing a hash is standing in for.
	Summary string
	// Line is the line number in the file as it is now; Orig is the line
	// number it had in the commit that introduced it.
	Line, Orig int
}

// Uncommitted reports whether the line has never been committed, which git
// blames on the all-zero hash.
func (b BlameLine) Uncommitted() bool {
	return strings.Trim(b.Hash, "0") == ""
}

// Blame runs `git blame` over a path and returns one entry per line.
//
// It reads --porcelain and not the human format, because the human format is
// a rendering -- the author column is padded to a width that depends on the
// other lines, the date format depends on the person's configuration, and a
// file name appears in it only sometimes -- and this package does its own
// rendering. The commit each line is blamed on is the same either way, which
// is what blame_test.go checks against the human format that `git blame`
// prints.
func (r *Repo) Blame(rel string, extra ...string) ([]BlameLine, error) {
	args := append([]string{"blame", "--porcelain"}, extra...)
	out, err := r.output(append(args, "--", rel)...)
	if err != nil {
		return nil, err
	}
	return parseBlame(out), nil
}

// parseBlame reads the porcelain blame format.
//
// The format is a header line -- "<hash> <orig-line> <final-line> [<count>]"
// -- then key/value lines, then the source line with a tab in front of it.
// The author, time and summary are only repeated the first time a commit is
// seen, so they are remembered per commit and filled in for every line after.
func parseBlame(out string) []BlameLine {
	type commit struct {
		author, zone, summary string
		when                  time.Time
		boundary              bool
	}
	seen := map[string]*commit{}
	var lines []BlameLine
	var cur *commit
	var hash string
	var orig, final int
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "\t"):
			if cur == nil {
				continue
			}
			lines = append(lines, BlameLine{
				Hash: hash, Abbrev: abbrev(hash, cur.boundary), Boundary: cur.boundary,
				Author: cur.author, When: cur.when, Zone: cur.zone,
				Summary: cur.summary, Line: final, Orig: orig,
			})
			cur = nil
		case len(line) > 40 && isHex(line[:40]) && line[40] == ' ':
			f := strings.Fields(line)
			hash = f[0]
			if len(f) > 2 {
				orig, final = atoi(f[1]), atoi(f[2])
			}
			c, ok := seen[hash]
			if !ok {
				c = &commit{}
				seen[hash] = c
			}
			cur = c
		case cur == nil:
			// A key line with no header in front of it. git does not write
			// one; a guard here is cheaper than a nil dereference the day
			// some future git does.
		case strings.HasPrefix(line, "author "):
			cur.author = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "author-time "):
			cur.when = time.Unix(int64(atoi(strings.TrimPrefix(line, "author-time "))), 0)
		case strings.HasPrefix(line, "author-tz "):
			cur.zone = strings.TrimPrefix(line, "author-tz ")
		case strings.HasPrefix(line, "summary "):
			cur.summary = strings.TrimPrefix(line, "summary ")
		case line == "boundary":
			cur.boundary = true
		}
	}
	return lines
}

// isHex reports whether every byte is a lowercase hex digit, which is what
// tells a blame header line from a key/value line whose key happens to be
// forty characters long.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// RenderBlame draws the blame column, byte for byte as `git blame` draws it
// with the source text taken off the end:
//
//	63a72f8e (T 17:13:33 -0700 1)
//	ce1679c6 (LongerName 17:13:33 -0700 13)
//
// The author is padded to the widest author, the line number is right
// aligned to the widest line number, the time is in the zone the commit was
// authored in, and a boundary commit is a "^" and one hex digit fewer so that
// every line is the same width. All of that is git's, measured against its
// output in blame_test.go rather than described here and hoped for.
//
// Dropping the source text is the judgment call, and it is where this parts
// company with fugitive, which keeps the text on the end and hides it by
// making the window narrower than the line. Keeping it means the file is in
// two buffers at once, so every scroll draws it twice, a yank out of the
// blame window takes the wrong copy, and the two get out of step the moment
// the file is edited. The window beside this one already has the text.
func RenderBlame(bl []BlameLine) []string {
	author, num := 0, 1
	for _, b := range bl {
		if n := len(b.Author); n > author {
			author = n
		}
		if n := len(itoa(b.Line)); n > num {
			num = n
		}
	}
	out := make([]string, len(bl))
	for i, b := range bl {
		out[i] = b.Abbrev + " (" + padRight(b.Author, author) + " " +
			b.When.In(zone(b.Zone)).Format("2006-01-02 15:04:05") + " " + b.Zone + " " +
			padLeft(itoa(b.Line), num) + ")"
	}
	return out
}

// zone turns git's "-0700" into a location, so that a blame line shows the
// time in the zone the commit was made in, which is what `git blame` shows.
func zone(tz string) *time.Location {
	if len(tz) != 5 || (tz[0] != '+' && tz[0] != '-') {
		return time.Local
	}
	off := (atoi(tz[1:3])*60 + atoi(tz[3:5])) * 60
	if tz[0] == '-' {
		off = -off
	}
	return time.FixedZone(tz, off)
}

func padLeft(s string, n int) string {
	for len(s) < n {
		s = " " + s
	}
	return s
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// abbrev is the commit id as `git blame` prints it: eight characters, or a
// "^" and seven for a boundary commit, so that both are eight columns wide.
func abbrev(hash string, boundary bool) string {
	if boundary {
		if len(hash) >= 7 {
			return "^" + hash[:7]
		}
		return "^" + hash
	}
	if len(hash) >= 8 {
		return hash[:8]
	}
	return hash
}
