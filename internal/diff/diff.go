// Package diff says what changed between two documents, field by field.
//
// # WHY A DIFF RATHER THAN INSTRUMENTED FORMS
//
// The obvious way to know what someone edited is to ask the control they typed
// in. It would also tie the history to the interface: a change made through the
// API, a layout switch, an import — none of those pass through a form, and none
// would be recorded. Worse, it would have to be re-taught for every template,
// when the whole point of the field tree is that no code names a section.
//
// Comparing the document before and after a write knows nothing about either.
// Whatever moved is reported, wherever it came from.
//
// A path addresses the NEW document, in the notation the rest of the engine
// already uses for errors:
//
//	content.identity.name
//	content.sections[2].items[0].bullets[1]
//
// # ARRAYS ARE THE HARD PART
//
// Compared position by position, inserting one bullet at the top reports every
// following bullet as rewritten — the history would be unreadable exactly when
// it matters. Elements are therefore aligned first: by id when they carry one,
// otherwise by longest common subsequence, so an insertion is an insertion and
// its neighbours are left alone.
package diff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Kind is what happened, in the vocabulary the interface knows how to show.
//
//	set     one value replaced by another
//	add     something that did not exist
//	remove  something that no longer exists
//	move    the same elements, in another order
type Kind string

const (
	Set    Kind = "set"
	Add    Kind = "add"
	Remove Kind = "remove"
	Move   Kind = "move"
)

// Change is one leaf that differs.
type Change struct {
	// Path is where, into the NEW document. Empty for the document itself.
	Path   string
	Kind   Kind
	Before any
	After  any
}

// isBlank folds absent, null and empty into one answer: nothing there.
//
// The generated forms send every field they draw, so a document saved for the
// first time gains a dozen empty strings where keys were simply missing. Those
// are not edits, and a history opening on twelve “(nothing) → (nothing)” lines
// would teach people to ignore it.
func isBlank(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

func isObj(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func isArr(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

// canon is the stable serialisation every comparison goes through.
//
// Key ORDER must not count as a change: documents are rebuilt by copying maps,
// and Go's iteration order is deliberately random, so comparing serialised JSON
// directly would report an edit on every save. Blank values are dropped for the
// same reason as above.
func canon(v any) string {
	if isBlank(v) {
		return "null"
	}
	if a, ok := isArr(v); ok {
		parts := make([]string, len(a))
		for i, x := range a {
			parts[i] = canon(x)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	if m, ok := isObj(v); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			if !isBlank(m[k]) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = quote(k) + ":" + canon(m[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// Same reports whether two values are the same, for the purposes above.
func Same(a, b any) bool { return canon(a) == canon(b) }

// Diff is every leaf that differs between two values.
func Diff(before, after any) []Change {
	var out []Change
	walk(before, after, "", &out)
	return out
}

func walk(a, b any, path string, out *[]Change) {
	if Same(a, b) {
		return
	}

	// One side is nothing: report the appearance or the disappearance whole,
	// rather than one line per field of what appeared. Adding an entry to the
	// Experience section is one event, not five.
	if isBlank(a) || isBlank(b) {
		kind := Set
		_, objA := isObj(a)
		_, objB := isObj(b)
		_, arrA := isArr(a)
		_, arrB := isArr(b)
		if objA || objB || arrA || arrB {
			kind = Add
			if isBlank(b) {
				kind = Remove
			}
		}
		*out = append(*out, Change{Path: path, Kind: kind, Before: a, After: b})
		return
	}

	if aa, ok := isArr(a); ok {
		if bb, ok := isArr(b); ok {
			walkArray(aa, bb, path, out)
			return
		}
	}
	if am, ok := isObj(a); ok {
		if bm, ok := isObj(b); ok {
			for _, key := range union(am, bm) {
				child := key
				if path != "" {
					child = path + "." + key
				}
				walk(am[key], bm[key], child, out)
			}
			return
		}
	}
	*out = append(*out, Change{Path: path, Kind: Set, Before: a, After: b})
}

func union(a, b map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		seen[k] = true
		out = append(out, k)
	}
	for k := range b {
		if !seen[k] {
			out = append(out, k)
		}
	}
	// Sorted so the journal of one save is written in the same order twice.
	sort.Strings(out)
	return out
}

func walkArray(a, b []any, path string, out *[]Change) {
	// Reordering is its own event: the same elements, elsewhere. Detected
	// before anything else, or dragging a section up would be reported as one
	// element rewritten into another and back down the list.
	if len(a) == len(b) {
		ka, kb := sortedCanon(a), sortedCanon(b)
		if ka == kb {
			*out = append(*out, Change{Path: path, Kind: Move, Before: a, After: b})
			return
		}
	}
	for _, pair := range align(a, b) {
		switch {
		case pair.from < 0:
			*out = append(*out, Change{
				Path: fmt.Sprintf("%s[%d]", path, pair.to), Kind: Add, After: b[pair.to]})
		case pair.to < 0:
			*out = append(*out, Change{
				Path: fmt.Sprintf("%s[%d]", path, pair.from), Kind: Remove, Before: a[pair.from]})
		default:
			walk(a[pair.from], b[pair.to], fmt.Sprintf("%s[%d]", path, pair.to), out)
		}
	}
}

func sortedCanon(v []any) string {
	keys := make([]string, len(v))
	for i, x := range v {
		keys[i] = canon(x)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// pair is one element of the old array against one of the new. A negative index
// means there is nothing on that side.
type pair struct{ from, to int }

// align works out which element of the old array corresponds to which of the
// new one.
//
// By id when every element carries a distinct one — that is exact, and it is
// the case for sections, the array people actually reorder. Otherwise by
// longest common subsequence over the unchanged elements, which anchors the
// untouched neighbours and leaves only the disturbed stretch to interpret.
func align(a, b []any) []pair {
	if ia, ok := identifiers(a); ok {
		if ib, ok := identifiers(b); ok {
			return alignByID(ia, ib)
		}
	}

	ka := make([]string, len(a))
	for i, x := range a {
		ka[i] = canon(x)
	}
	kb := make([]string, len(b))
	for i, x := range b {
		kb[i] = canon(x)
	}

	var out []pair
	i, j := 0, 0
	// Between two anchors, what is left on each side is paired up in order.
	// That is what turns “one bullet removed, one bullet added” into “this
	// bullet was rewritten”, which is what actually happened when someone edits
	// one.
	gap := func(iEnd, jEnd int) {
		left, right := iEnd-i, jEnd-j
		paired := min(left, right)
		for k := 0; k < paired; k++ {
			out = append(out, pair{i + k, j + k})
		}
		for k := paired; k < left; k++ {
			out = append(out, pair{i + k, -1})
		}
		for k := paired; k < right; k++ {
			out = append(out, pair{-1, j + k})
		}
	}
	for _, anchor := range lcs(ka, kb) {
		gap(anchor.from, anchor.to)
		out = append(out, anchor)
		i, j = anchor.from+1, anchor.to+1
	}
	gap(len(a), len(b))
	return out
}

// identifiers is the ids of an array of objects, when they all have a distinct
// non-empty one.
func identifiers(arr []any) ([]string, bool) {
	ids := make([]string, 0, len(arr))
	seen := map[string]bool{}
	for _, v := range arr {
		m, ok := isObj(v)
		if !ok {
			return nil, false
		}
		id, ok := m["id"].(string)
		if !ok || id == "" || seen[id] {
			return nil, false
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

func alignByID(ia, ib []string) []pair {
	var out []pair
	taken := map[int]bool{}
	for j, id := range ib {
		at := -1
		for i, candidate := range ia {
			if candidate == id {
				at = i
				break
			}
		}
		if at < 0 {
			out = append(out, pair{-1, j})
			continue
		}
		taken[at] = true
		out = append(out, pair{at, j})
	}
	for i := range ia {
		if !taken[i] {
			out = append(out, pair{i, -1})
		}
	}
	return out
}

// lcs is the index pairs of the longest common subsequence. The arrays here
// hold tens of items, so the quadratic table costs nothing.
func lcs(ka, kb []string) []pair {
	n, m := len(ka), len(kb)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if ka[i] == kb[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out []pair
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case ka[i] == kb[j]:
			out = append(out, pair{i, j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return out
}
