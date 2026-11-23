package jev

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koltyakov/decide/internal/fault"
)

// referenceMatcher finds asset references as whole tokens. A reference
// edge that is a letter, digit, '_' or '-' must not touch another such
// character, so "photo" does not match inside "photography" or "photo-2".
type referenceMatcher struct {
	byFirst [256][]string
	first   [256]bool
}

func newReferenceMatcher(refs []string) *referenceMatcher {
	m := &referenceMatcher{}
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		m.byFirst[ref[0]] = append(m.byFirst[ref[0]], ref)
		m.first[ref[0]] = true
	}
	for i := range m.byFirst {
		// Longest first, so the leftmost match is also the longest.
		sort.Slice(m.byFirst[i], func(a, b int) bool { return len(m.byFirst[i][a]) > len(m.byFirst[i][b]) })
	}
	return m
}

func wordRune(r rune) bool { return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func (m *referenceMatcher) matchAt(s string, i int, ref string) bool {
	if !strings.HasPrefix(s[i:], ref) {
		return false
	}
	if first, _ := utf8.DecodeRuneInString(ref); wordRune(first) && i > 0 {
		if before, _ := utf8.DecodeLastRuneInString(s[:i]); wordRune(before) {
			return false
		}
	}
	end := i + len(ref)
	if last, _ := utf8.DecodeLastRuneInString(ref); wordRune(last) && end < len(s) {
		if after, _ := utf8.DecodeRuneInString(s[end:]); wordRune(after) {
			return false
		}
	}
	return true
}

// next returns the leftmost, longest whole-token reference at or after from.
func (m *referenceMatcher) next(s string, from int) (int, string) {
	for i := from; i < len(s); i++ {
		if !m.first[s[i]] {
			continue
		}
		for _, ref := range m.byFirst[s[i]] {
			if m.matchAt(s, i, ref) {
				return i, ref
			}
		}
	}
	return -1, ""
}

// collect marks every reference that occurs in string values of value.
// Object keys are names, not reference locations.
func (m *referenceMatcher) collect(value any, found map[string]bool, remaining *int) {
	if *remaining == 0 {
		return
	}
	switch v := value.(type) {
	case string:
		for i := 0; i < len(v) && *remaining > 0; i++ {
			if !m.first[v[i]] {
				continue
			}
			for _, ref := range m.byFirst[v[i]] {
				if !found[ref] && m.matchAt(v, i, ref) {
					found[ref] = true
					*remaining--
				}
			}
		}
	case []any:
		for _, item := range v {
			m.collect(item, found, remaining)
		}
	case map[string]any:
		for _, item := range v {
			m.collect(item, found, remaining)
		}
	}
}

// unreferenced returns refs that occur in none of values, in sorted order.
func unreferenced(refs []string, values ...any) []string {
	if len(refs) == 0 {
		return nil
	}
	m := newReferenceMatcher(refs)
	found := map[string]bool{}
	remaining := len(refs)
	for _, value := range values {
		m.collect(value, found, &remaining)
	}
	var missing []string
	for _, ref := range refs {
		if !found[ref] {
			missing = append(missing, ref)
		}
	}
	sort.Strings(missing)
	return missing
}

// questionValues lists the question members that may contain references.
func questionValues(questions any) []any {
	var values []any
	q, _ := questions.(map[string]any)
	for _, v := range q {
		question, _ := v.(map[string]any)
		values = append(values, question["instructions"], question["criteria"])
	}
	return values
}

var errUnreferencedAsset = fault.New(fault.Usage, "every asset must be referenced as a whole token in state, instructions, or criteria")
