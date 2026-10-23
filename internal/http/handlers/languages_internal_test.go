package handlers

import "testing"

func TestParsePackRefusesOversizedFiles(t *testing.T) {
	if _, msg, err := parsePack(make([]byte, MaxPackSize+1)); err == nil || msg != "" {
		t.Error("parsePack took an oversized file")
	}
}
