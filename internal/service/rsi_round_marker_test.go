package service

import "testing"

func TestRSIRoundMarker(t *testing.T) {
	if RSIRoundMarker != 1 {
		t.Fatalf("RSIRoundMarker = %d, want 1", RSIRoundMarker)
	}
}
