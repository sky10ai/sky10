package kv

func filterKeysByPattern(keys []string, pattern string) []string {
	matched := make([]string, 0, len(keys))
	for _, key := range keys {
		if matchPattern(pattern, key) {
			matched = append(matched, key)
		}
	}
	return matched
}

// matchPattern applies a small glob syntax where '*' matches any sequence of
// characters, including '/', and '?' matches a single character.
func matchPattern(pattern, key string) bool {
	pat := []rune(pattern)
	text := []rune(key)

	p, k := 0, 0
	star := -1
	match := 0

	for k < len(text) {
		switch {
		case p < len(pat) && (pat[p] == '?' || pat[p] == text[k]):
			p++
			k++
		case p < len(pat) && pat[p] == '*':
			star = p
			match = k
			p++
		case star != -1:
			p = star + 1
			match++
			k = match
		default:
			return false
		}
	}

	for p < len(pat) && pat[p] == '*' {
		p++
	}

	return p == len(pat)
}
