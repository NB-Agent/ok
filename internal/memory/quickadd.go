package memory

import (
	"os"
	"path/filepath"
	"strings"
)

// quickAddHeading marks the section quick-added notes accumulate under, so
// repeated "#" additions group together instead of scattering through a
// hand-written file.
const quickAddHeading = "## Notes"

// MaxQuickNotes caps the number of bullet entries under ## Notes in a
// doc-memory file. Older entries are trimmed when new ones are added beyond
// this limit, preventing unbounded prefix bloat.
const MaxQuickNotes = 30

// maxQuickNoteLen caps the character length of a single quick-add note.
// Notes exceeding this are truncated to keep the system-prompt prefix lean.
const maxQuickNoteLen = 200

// AppendDoc appends a one-line note as a bullet under a "## Notes" section in
// the doc-memory file at path, creating the file (and section) when absent. The
// note is normalised to a single line so it can't corrupt the section. This is
// the write side of the "#" quick-add: a plain file edit the user can later
// reorganise by hand.
//
// Uses a temp-file + rename to avoid TOCTOU: concurrent quick-adds or external
// edits between read and write are visible as a rename failure instead of a
// silent overwrite.
func AppendDoc(path, note string) error {
	note = oneLine(note)
	if len(note) > maxQuickNoteLen {
		note = note[:maxQuickNoteLen] + "…"
	}
	if note == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err // permission error, etc. — don't silently overwrite
	}
	body := string(existing)
	bullet := "- " + note

	var out string
	switch {
	case strings.TrimSpace(body) == "":
		out = "# Project memory\n\n" + quickAddHeading + "\n\n" + bullet + "\n"
	case strings.Contains(body, quickAddHeading):
		out = insertUnderHeading(body, quickAddHeading, bullet)
	default:
		out = strings.TrimRight(body, "\n") + "\n\n" + quickAddHeading + "\n\n" + bullet + "\n"
	}

	// Write to a per-doc temp file and rename atomically to avoid TOCTOU races.
	// Use a random suffix so concurrent AppendDoc calls don't clobber each other's temp file.
	tmp := path + "." + randHex(4) + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeDocFile overwrites path with body, creating the parent directory and
// ensuring a single trailing newline. Used by Set.WriteDoc for the panel's
// in-place editor (path validation happens in the caller).
func writeDocFile(path, body string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	out := strings.TrimRight(body, "\n") + "\n"
	return os.WriteFile(path, []byte(out), 0o644)
}

// insertUnderHeading appends bullet to the end of the section started by heading
// — just before the next "## "/"# " heading, or at end of file if none follows.
// If the section exceeds MaxQuickNotes bullets, the oldest entries are trimmed.
func insertUnderHeading(body, heading, bullet string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == heading {
			start = i
			break
		}
	}
	if start < 0 { // shouldn't happen (caller checked Contains), but stay safe
		return strings.TrimRight(body, "\n") + "\n\n" + bullet + "\n"
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			end = i
			break
		}
	}
	// Trim trailing blank lines within the section, then place the bullet.
	insert := end
	for insert > start+1 && strings.TrimSpace(lines[insert-1]) == "" {
		insert--
	}
	out := append([]string{}, lines[:insert]...)
	out = append(out, bullet)
	out = append(out, lines[insert:]...)

	// Enforce cap: find the section in the output, count bullet lines,
	// and remove the oldest (first) if there are too many.
	secStart := -1
	for i, l := range out {
		if strings.TrimSpace(l) == heading {
			secStart = i
			break
		}
	}
	if secStart >= 0 {
		secEnd := len(out)
		for i := secStart + 1; i < len(out); i++ {
			if strings.HasPrefix(strings.TrimSpace(out[i]), "#") {
				secEnd = i
				break
			}
		}
		// Collect bullet line indices within the section.
		var bullets []int
		for i := secStart + 1; i < secEnd; i++ {
			if strings.HasPrefix(strings.TrimSpace(out[i]), "-") {
				bullets = append(bullets, i)
			}
		}
		if len(bullets) > MaxQuickNotes {
			overflow := len(bullets) - MaxQuickNotes
			keep := make([]string, 0, secStart+1+MaxQuickNotes+(len(out)-secEnd))
			keep = append(keep, out[:secStart+1]...)
			for i := overflow; i < len(bullets); i++ {
				keep = append(keep, out[bullets[i]])
			}
			keep = append(keep, out[secEnd:]...)
			out = keep
		}
	}
	return strings.Join(out, "\n")
}
