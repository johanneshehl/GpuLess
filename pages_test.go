package main

import "testing"

func TestAssetTag(t *testing.T) {
	a, b := assetTag(), assetTag()
	if len(a) != 10 || a != b {
		t.Fatalf("asset tag should be a stable 10-character hash, got %q and %q", a, b)
	}
}
