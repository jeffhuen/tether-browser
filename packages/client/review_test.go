package client

import (
	"os"
	"testing"
)

func TestReviewOverlayCopiesMatch(t *testing.T) {
	extensionScript, err := os.ReadFile("../extension/review/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	if string(extensionScript) != reviewOverlayScript {
		t.Fatal("review overlay copies differ; run: cp packages/extension/review/overlay.js packages/client/review/")
	}
}
