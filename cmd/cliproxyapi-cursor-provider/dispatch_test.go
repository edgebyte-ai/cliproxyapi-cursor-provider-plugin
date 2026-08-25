package main

import "testing"

func TestPluginRegistrationName(t *testing.T) {
	registration := pluginRegistration()
	if registration.Metadata.Name != "Cursor Provider" {
		t.Fatalf("plugin name = %q, want Cursor Provider", registration.Metadata.Name)
	}
}
