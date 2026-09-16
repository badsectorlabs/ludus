package ludusapi

import (
	"testing"

	"ludusapi/pluginrpc"
)

func TestApplyPluginStatePreservesEntitlementsOnEmptyValidState(t *testing.T) {
	server := &Server{
		Entitlements: []string{"ENTERPRISE_PLUGIN", "ANTISANDBOX_PLUGIN"},
		LicenseValid: true,
		LicenseKey:   "host-key",
	}

	server.applyPluginState(pluginrpc.ServerState{
		Entitlements:   nil,
		LicenseValid:   true,
		LicenseMessage: "Unable to connect to license server (used fallback)",
		LicenseKey:     "host-key",
		LicenseName:    "fallback",
	})

	if !server.HasEntitlement("ENTERPRISE_PLUGIN") || !server.HasEntitlement("ANTISANDBOX_PLUGIN") {
		t.Fatalf("expected host entitlements to be preserved, got %v", server.Entitlements)
	}
	if !server.LicenseValid {
		t.Fatal("expected license to remain valid")
	}
	if server.LicenseMessage != "Unable to connect to license server (used fallback)" {
		t.Fatalf("unexpected license message: %q", server.LicenseMessage)
	}
}

func TestApplyPluginStateReplacesEntitlementsWhenProvided(t *testing.T) {
	server := &Server{
		Entitlements: []string{"ENTERPRISE_PLUGIN"},
		LicenseValid: true,
	}

	server.applyPluginState(pluginrpc.ServerState{
		Entitlements: []string{"ENTERPRISE_PLUGIN", "ANTISANDBOX_PLUGIN"},
		LicenseValid: true,
	})

	if !server.HasEntitlement("ANTISANDBOX_PLUGIN") {
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
