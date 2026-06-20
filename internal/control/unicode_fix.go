//go:build ignore

package main

import (
	"bytes"
	"os"
)

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	// The corruption pattern: two U+FFFD (replacement char) followed by '?'
	// comes from the original " — " (U+2014 EM DASH) being corrupted by
	// PowerShell's Set-Content default encoding.
	// Replace: \ufffd\ufffd?  →  \u2014 (em dash, followed by space if needed)

	// Pattern 1: "��?"  → " —"
	corrupt1 := []byte{0xEF, 0xBF, 0xBD, 0xEF, 0xBF, 0xBD, 0x3F}
	replacement1 := []byte{0xE2, 0x80, 0x94} // "—" (em dash, 3 bytes in UTF-8)
	data = bytes.ReplaceAll(data, corrupt1, replacement1)

	// Pattern 2: "��?" followed by "//" → "— //" (keep the space)
	// Already handled above since replacement1 is just the em dash

	// Pattern 3: "鈫?" (appears when → got corrupted)
	// The arrow → is E2 86 92 in UTF-8
	// After corruption by PowerShell it may appear as different bytes
	// Let's try to find it: look for the specific corrupted pattern
	corrupt2 := []byte{0xE9, 0x88, 0xAB, 0x3F} // "鈫?"
	replacement2 := []byte{0xE2, 0x86, 0x92}   // "→"
	data = bytes.ReplaceAll(data, corrupt2, replacement2)

	os.WriteFile(os.Args[1], data, 0644)
}
