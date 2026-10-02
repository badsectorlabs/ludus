package ludusapi

import (
	"testing"

	"ludusapi/pluginrpc"
)

func TestApplyPluginStateClearsRevokedEntitlementsOnValidLicense(t *testing.T) {
	server := &Server{
		Entitlements: []string{"ENTERPRISE_PLUGIN", "EXAMPLE_ADDON"},
		LicenseValid: true,
		LicenseKey:   "host-key",
	}

	server.applyPluginState(pluginrpc.ServerState{
		Entitlements:   nil,
		LicenseValid:   true,
		LicenseMessage: "License active without add-ons",
		LicenseKey:     "host-key",
		LicenseName:    "base",
	})

	if server.HasEntitlement("ENTERPRISE_PLUGIN") || server.HasEntitlement("EXAMPLE_ADDON") {
		t.Fatalf("expected revoked entitlements to be cleared, got %v", server.Entitlements)
	}
	if !server.LicenseValid {
		t.Fatal("expected license to remain valid")
	}
}

func TestApplyPluginStateReplacesEntitlementsWhenProvided(t *testing.T) {
	server := &Server{
		Entitlements: []string{"ENTERPRISE_PLUGIN"},
		LicenseValid: true,
	}

	server.applyPluginState(pluginrpc.ServerState{
		Entitlements: []string{"ENTERPRISE_PLUGIN", "EXAMPLE_ADDON"},
		LicenseValid: true,
	})

	if !server.HasEntitlement("EXAMPLE_ADDON") {
		t.Fatalf("expected replaced entitlements, got %v", server.Entitlements)
	}
}

func TestApplyPluginStateClearsEntitlementsWhenLicenseInvalid(t *testing.T) {
	server := &Server{
		Entitlements: []string{"ENTERPRISE_PLUGIN"},
		LicenseValid: true,
	}

	server.applyPluginState(pluginrpc.ServerState{
		Entitlements:   nil,
		LicenseValid:   false,
		LicenseMessage: "License is expired",
	})

	if len(server.Entitlements) != 0 {
		t.Fatalf("expected entitlements cleared for invalid license, got %v", server.Entitlements)
	}
	if server.LicenseValid {
		t.Fatal("expected license to be invalid")
	}
}
