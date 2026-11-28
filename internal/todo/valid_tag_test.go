package todo

import (
	"strings"
	"testing"
)

func TestValidTag(t *testing.T) {
	valid := []string{"a", "casa", "a_b-c", "0day", strings.Repeat("x", MaxTagLength)}
	invalid := []string{"", "Casa", "-lead", "_lead", "café", "has space", "#tag", strings.Repeat("x", MaxTagLength+1)}
	for _, tag := range valid {
		if !ValidTag(tag) {
			t.Errorf("ValidTag(%q) = false, want true", tag)
		}
	}
	for _, tag := range invalid {
		if ValidTag(tag) {
			t.Errorf("ValidTag(%q) = true, want false", tag)
		}
	}
}
