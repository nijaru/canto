package target

// ContainsAny checks if the string s contains any of the substrings in subs.
// This is a deliberately naive implementation for the autoresearch example to optimize.
func ContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
