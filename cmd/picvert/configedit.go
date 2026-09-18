package main

// Editing the configuration file in place, for `picvert passwd --write`.
//
// # WHY NOT MARSHAL THE STRUCT BACK OUT
//
// Because the file is not a serialised struct, it is a document. It is mostly
// comments — the reasons behind each setting, which is where this project puts
// its reasoning — and round-tripping it through a YAML encoder returns a file
// with every one of them gone, the keys reordered, and the defaults that were
// left implicit now written out explicitly. That is not saving a setting, it is
// replacing somebody's file with a machine's idea of the same settings.
//
// So one line is rewritten and every other byte is left exactly as it was.
//
// # WHY IN PLACE, AND NOT THE USUAL TEMP-FILE-AND-RENAME
//
// Everywhere else in this program a file is written to a temporary name and
// renamed over the target, because that cannot leave a half-written file behind.
// Here it would break the service.
//
// /etc/picvert.yaml is root:picvert 0640 — owned by root, READABLE BY THE
// SERVICE ACCOUNT, which is how a process running as `picvert` reads a file
// that it must not be able to edit. A fresh file created by root and renamed
// into place is root:root, and the service then cannot read its own
// configuration and will not start. Preserving the inode preserves the owner,
// the group, the mode, and anything else hung off it.
//
// The torn-write risk that temp-and-rename exists to remove is covered by
// writing the .bak first: there is always a complete copy on disk.

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// setScalar rewrites `key:` inside the top-level block `section`, and returns
// the whole file.
//
// Line surgery rather than a parser, because a parser that can write is a
// parser that reformats. The rules are deliberately narrow: the key must be
// directly inside the named block, and must not be commented out.
//
// # THE TWO WAYS A NAIVE VERSION OF THIS GETS IT WRONG
//
// Searching for the text "password" in picvert.yaml finds, in order: a line of
// prose in a comment, a commented-out EXAMPLE showing the shape of a hash, the
// real setting, and `min-password-length` — which contains the word. Three of
// those four must not be touched, and the last one is a different setting
// entirely whose value would be replaced by a password hash.
//
// Anchoring on the indentation and requiring the colon immediately after the
// name excludes all three: prose does not start with `password:`, a comment
// starts with `#`, and `min-password-length:` does not start with `password:`.
func setScalar(source []byte, section, key, value string) []byte {
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	trailing := bytes.HasSuffix(source, []byte(newline))
	lines := strings.Split(strings.TrimSuffix(string(source), newline), newline)

	put := func(indent string) string {
		return indent + key + ": " + quote(value)
	}

	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == section+":" {
			start = i
			break
		}
	}

	// No such block: add it at the end, which is the only place that cannot
	// land in the middle of somebody else's.
	if start < 0 {
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, section+":", put("  "))
		return join(out, newline, true)
	}

	// The block runs until a line that is neither blank nor indented.
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if lines[i][0] != ' ' && lines[i][0] != '\t' {
			end = i
			break
		}
	}

	for i := start + 1; i < end; i++ {
		indent := leading(lines[i])
		rest := lines[i][len(indent):]
		if indent == "" || strings.HasPrefix(rest, "#") {
			continue
		}
		if strings.HasPrefix(rest, key+":") {
			out := append([]string{}, lines...)
			out[i] = put(indent)
			return join(out, newline, trailing)
		}
	}

	// Present but unset. Added at the top of its block rather than the bottom,
	// so it lands next to the comments that explain it rather than after
	// whatever happens to be last.
	indent := "  "
	for i := start + 1; i < end; i++ {
		if got := leading(lines[i]); got != "" && strings.TrimSpace(lines[i]) != "" {
			indent = got
			break
		}
	}
	out := append([]string{}, lines[:start+1]...)
	out = append(out, put(indent))
	out = append(out, lines[start+1:]...)
	return join(out, newline, trailing)
}

func join(lines []string, newline string, trailing bool) []byte {
	s := strings.Join(lines, newline)
	if trailing {
		s += newline
	}
	return []byte(s)
}

func leading(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// quote writes a YAML double-quoted scalar.
//
// A pbkdf2 hash is base64 and separators — no backslash and no quote ever
// appears in one. Escaped anyway: a function that is only correct for the
// input it happens to get today is a function that is wrong later, quietly.
func quote(value string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(value) + `"`
}

// writeInPlace saves the file over itself, keeping a .bak of what was there.
//
// See the note at the top of this file for why the inode is kept rather than
// replaced.
func writeInPlace(path string, content []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Written and flushed BEFORE the original is touched. This is the copy
	// that makes overwriting in place safe, so it has to exist on disk first.
	backup := path + ".bak"
	if err := os.WriteFile(backup, previous, info.Mode().Perm()); err != nil {
		return fmt.Errorf("could not save %s: %w", backup, err)
	}
	if err := os.WriteFile(path, content, info.Mode().Perm()); err != nil {
		return fmt.Errorf("%s is half-written — put %s back: %w", path, backup, err)
	}
	return nil
}
