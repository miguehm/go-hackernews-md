package api

import "encoding/json"

// marshalWithin encodes v (a pointer to the response) and, while the result
// is larger than limit, calls shrink to drop data from the tail and encodes
// again. shrink reports whether it removed anything; the loop stops when it
// cannot. The second result reports whether anything was dropped.
func marshalWithin(limit int, v any, shrink func() bool) ([]byte, bool, error) {
	data, err := json.Marshal(v)
	truncated := false
	for err == nil && len(data) > limit && shrink() {
		truncated = true
		data, err = json.Marshal(v)
	}
	return data, truncated, err
}

// dropLast removes the last element of *s, reporting false when it is empty.
func dropLast[T any](s *[]T) bool {
	if len(*s) == 0 {
		return false
	}
	*s = (*s)[:len(*s)-1]
	return true
}
