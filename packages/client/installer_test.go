package client

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeHostManifestFormat(t *testing.T) {
	manifest := NativeHostManifest{
		Name:        NativeHostName,
		Description: "Tether Native Messaging Host",
		Path:        "/usr/local/bin/tether",
		Type:        "stdio",
		AllowedOrigins: []string{
			"chrome-extension://" + ExtensionID + "/",
			"chrome-extension://" + WebStoreExtensionID + "/",
		},
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	if !strings.Contains(string(data), NativeHostName) {
		t.Fatalf("expected manifest to contain host name %s", NativeHostName)
	}
	if !strings.Contains(string(data), ExtensionID) {
		t.Fatalf("expected manifest to contain extension ID %s", ExtensionID)
	}
	if !strings.Contains(string(data), WebStoreExtensionID) {
		t.Fatalf("expected manifest to contain Web Store extension ID %s", WebStoreExtensionID)
	}
	if len(ExtensionID) != 32 {
		t.Fatalf("Chrome extension ID must be exactly 32 chars, got: %d", len(ExtensionID))
	}
	if len(WebStoreExtensionID) != 32 {
		t.Fatalf("Web Store extension ID must be exactly 32 chars, got: %d", len(WebStoreExtensionID))
	}
}
