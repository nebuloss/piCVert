// Package quota is what stops a CV, or a disk, growing without end.
//
// # WHAT IS ALREADY BOUNDED, AND WHAT IS NOT
//
// Per-request limits exist and work: eight megabytes of body, four of portrait.
// The journal bounds itself at five hundred entries and merges an episode of
// typing into one, so a minute at the keyboard adds two lines rather than forty.
//
// What nothing bounded was the TOTAL. A profile is a directory, and directories
// grow — most obviously by language, since asking for one costs a request and
// yields a whole second document with its own journal. Four hundred were
// accepted in a few seconds, and only because the asking stopped.
//
// # TWO CEILINGS, BECAUSE THERE ARE TWO WAYS TO RUN OUT
//
//	PER PROFILE   one CV using more than its share. Bounded in bytes, because
//	              bytes are what the disk runs out of — a count of languages or
//	              of entries bounds the wrong thing and needs a new rule for
//	              every new kind of file.
//
//	FREE SPACE    the disk filling for any reason at all, including reasons
//	              that have nothing to do with this service. A refusal with an
//	              explanation beats a write that half-succeeds and a CV that
//	              will not parse.
//
// # WHY IT REFUSES RATHER THAN DELETES
//
// Nothing here ever removes anything to make room. What would be removed is
// somebody's CV, and a service that quietly discards work to stay within a
// limit is worse than one that says it is full.
package quota

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Limits are the two ceilings, read from the environment on every check so a
// running service can be adjusted without being rebuilt.
type Limits struct {
	// MaxProfileBytes is what one CV may occupy: its documents, its journals
	// and its portrait.
	//
	// Eight megabytes. A portrait is capped at four, the journal tops out near
	// two thirds of one, and a document is a few kilobytes — so this is roughly
	// twice what an ordinary CV with a photo and a couple of languages needs,
	// and far below what four hundred languages would have taken.
	MaxProfileBytes int64
	// MinFreeBytes is how much room must remain after a write.
	//
	// Not zero: a disk with nothing left is a disk on which the next write of
	// ANY kind fails, including the log that would say so.
	MinFreeBytes int64
}

func FromEnv() Limits {
	return Limits{
		MaxProfileBytes: envBytes("PICVERT_MAX_PROFILE_MB", 8) << 20,
		MinFreeBytes:    envBytes("PICVERT_MIN_FREE_MB", 64) << 20,
	}
}

func envBytes(name string, fallback int64) int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64); err == nil && v > 0 {
		return v
	}
	return fallback
}

// Error is a refusal for want of room, so a caller can answer 507 rather than
// 400 — "this is full" is not "what you sent is wrong".
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// Check refuses a write that would take a profile past its share, or the disk
// past its floor.
//
// adding is what the write is about to add, which is not the same as the size
// of what is being written: a document usually REPLACES one, so most writes add
// nothing at all. Passing the whole size would refuse a CV that has been at its
// ceiling for months and is merely being edited.
func Check(dir string, adding int64, limits Limits) error {
	if free, err := freeBytes(dir); err == nil && free-adding < limits.MinFreeBytes {
		return &Error{Msg: fmt.Sprintf(
			"the disk is nearly full (%d MB free, %d MB must remain)",
			free>>20, limits.MinFreeBytes>>20)}
	}
	used := DirSize(dir)
	if used+adding > limits.MaxProfileBytes {
		return &Error{Msg: fmt.Sprintf(
			"this CV has reached its limit (%d MB of %d MB) — remove a language "+
				"or a portrait to make room",
			used>>20, limits.MaxProfileBytes>>20)}
	}
	return nil
}

// DirSize is what a directory occupies, in bytes.
//
// Walked rather than remembered, because remembering means a counter that can
// drift from the files it claims to describe — and the moment it drifts is the
// moment somebody is refused a write for space they are not using. A profile
// holds a handful of files.
func DirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
