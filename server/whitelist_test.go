package server

import "testing"

func TestXxx(t *testing.T) {
	err := generateWhitelist()
	if err != nil {
		t.Error(err)
	}
}
