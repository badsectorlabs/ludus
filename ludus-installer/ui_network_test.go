package main

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func suggestionUIFixture(t *testing.T, change func(*Options, *Inventory)) *installerFormModel {
	t.Helper()
	m, _ := installerUIFixture(t, func(opts *Options, inv *Inventory) {
		opts.IP, opts.Gateway = "", ""
		inv.Bridges = append(inv.Bridges, "vmbr1")
		if change != nil {
			change(opts, inv)
		}
	})
	t.Cleanup(m.cancelNetworkSuggestion)
	focusInstallerUIField(m, uiVLANTag)
	return m
}

func suggestionResult(m *installerFormModel) networkSuggestionMsg {
	return networkSuggestionMsg{
		request: m.network.request, bridge: m.values[uiBridge], vlan: m.values[uiVLANTag],
		suggestion: managementAddressSuggestion{IP: "192.0.2.2/24", Gateway: "192.0.2.1"},
	}
}

func TestInstallerSuggestsOnlyAfterNetworkConfirmation(t *testing.T) {
	m := suggestionUIFixture(t, nil)
	if m.network.pending || m.values[uiIP] != "" {
		t.Fatal("must wait for the bridge to be confirmed")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.network.pending || m.focusedIndex() != uiIP {
		t.Fatal("confirmed VLAN must start an asynchronous suggestion at the IP field")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.focusedIndex() != uiIP || m.errorText != "" {
		t.Fatal("pending suggestion must not advance or mark an untouched IP invalid")
	}
	m.Update(suggestionResult(m))
	if m.network.pending || m.values[uiIP] != "192.0.2.2/24" || m.values[uiGateway] != "192.0.2.1" {
		t.Fatal("current suggestion must populate the empty IP and gateway")
	}
	if m.network.manualIP || m.network.manualGateway || m.settingConfirmations[uiIP].active {
		t.Fatal("automatic suggestions must not count as user edits or confirmations")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.focusedIndex() != uiGateway || !m.settingConfirmations[uiIP].active {
		t.Fatal("user acceptance of a suggestion must validate, advance and flash")
	}
}

func TestInstallerSuggestsAsSoonAsTheBridgeIsConfirmed(t *testing.T) {
	m := suggestionUIFixture(t, nil)
	focusInstallerUIField(m, uiBridge)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.network.pending || m.focusedIndex() != uiVLANTag {
		t.Fatal("confirming the bridge must start a suggestion for the untagged network")
	}
	request := m.network.request
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.network.pending || m.network.request != request {
		t.Fatal("confirming an unchanged VLAN must keep the probe already running")
	}
	// A tagged VLAN is a different network: the untagged probe is discarded
	// and confirming the VLAN starts a probe for the new network.
	focusInstallerUIField(m, uiVLANTag)
	m.Update(tea.PasteMsg{Content: "20"})
	if m.network.pending {
		t.Fatal("changing the VLAN must cancel the untagged probe")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.network.pending || m.network.vlan != "20" {
		t.Fatal("confirming a new VLAN must probe that VLAN")
	}
}

func TestInstallerSuggestionPreservesExplicitNetwork(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Options, *Inventory)
	}{
		{"explicit IP", func(o *Options, _ *Inventory) { o.IP = "192.0.2.77/24" }},
		{"explicit empty IP", func(o *Options, _ *Inventory) { o.Provided["ip"] = true }},
		{"migration", func(o *Options, _ *Inventory) { o.MigrateHost = true }},
		{"unattended", func(o *Options, _ *Inventory) { o.NoPrompt = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := suggestionUIFixture(t, test.change)
			original := m.values[uiIP]
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.network.pending || m.values[uiIP] != original {
				t.Fatal("suggestion must not alter explicit, migrated or unattended addressing")
			}
		})
	}
	m := suggestionUIFixture(t, func(o *Options, _ *Inventory) { o.Gateway = "192.0.2.254" })
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(suggestionResult(m))
	if m.values[uiIP] != "192.0.2.2/24" || m.values[uiGateway] != "192.0.2.254" {
		t.Fatal("IP suggestion must preserve an explicitly supplied gateway")
	}
}

func TestInstallerSuggestionCannotOverwriteTypingOrClear(t *testing.T) {
	m := suggestionUIFixture(t, nil)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := suggestionResult(m)
	m.Update(tea.PasteMsg{Content: "192.0.2.80/24"})
	m.Update(result)
	if m.network.pending || m.values[uiIP] != "192.0.2.80/24" {
		t.Fatal("late discovery must not overwrite a typed address")
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(result)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.network.pending || m.values[uiIP] != "" {
		t.Fatal("explicitly clearing an address must not revive an old suggestion")
	}
}

func TestInstallerNetworkChangeInvalidatesSuggestions(t *testing.T) {
	for _, field := range []int{uiBridge, uiVLANTag} {
		m := suggestionUIFixture(t, nil)
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		old := suggestionResult(m)
		m.Update(old)
		if field == uiBridge {
			m.values[field] = "vmbr1"
		} else {
			m.values[field] = "20"
		}
		m.synchronize()
		if m.values[uiIP] != "" || m.values[uiGateway] != "" {
			t.Fatal("a different bridge/VLAN must discard unedited automatic addressing")
		}
		focusInstallerUIField(m, uiVLANTag)
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m.Update(old)
		if !m.network.pending || m.values[uiIP] != "" {
			t.Fatal("a previous network's result must not replace the new pending request")
		}
		current := suggestionResult(m)
		current.suggestion.IP = "198.51.100.2/24"
		current.suggestion.Gateway = "198.51.100.1"
		m.Update(current)
		if m.values[uiIP] != current.suggestion.IP || m.values[uiGateway] != current.suggestion.Gateway {
			t.Fatal("only the selected network's result may populate the form")
		}
	}
}

func TestInstallerNetworkChangeKeepsUserEdits(t *testing.T) {
	m := suggestionUIFixture(t, nil)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(suggestionResult(m))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "192.0.2.90/24"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "192.0.2.254"})
	m.values[uiBridge] = "vmbr1"
	m.synchronize()
	if m.values[uiIP] != "192.0.2.90/24" || m.values[uiGateway] != "192.0.2.254" {
		t.Fatal("switching networks must not silently discard manually edited values")
	}
}

func TestInstallerSuggestionFailureAllowsManualEntry(t *testing.T) {
	m := suggestionUIFixture(t, nil)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := suggestionResult(m)
	result.err = errors.New("no matching active subnet")
	m.Update(result)
	if m.network.pending || m.values[uiIP] != "" || m.values[uiGateway] != "" || m.focusedIndex() != uiIP {
		t.Fatal("unavailable suggestion must leave an editable empty IP, not apply partial results")
	}
	m.Update(tea.PasteMsg{Content: "192.0.2.80/24"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.focusedIndex() != uiGateway || m.errorText != "" {
		t.Fatal("suggestion errors must not prevent manual static addressing")
	}
}

func TestInstallerRapidConfirmationCannotSkipRequiredNetwork(t *testing.T) {
	m := suggestionUIFixture(t, nil)
	focusInstallerUIField(m, uiRootFSSize)
	for range 6 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if m.focusedIndex() != uiIP || !m.network.pending || m.review {
		t.Fatal("a burst of confirmations must stop at the pending empty IP, not skip groups")
	}
	for range 3 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	}
	if m.focusedIndex() != uiRootFSSize {
		t.Fatal("rapid backward navigation must cross exactly one group boundary")
	}
	for range 3 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if m.focusedIndex() != uiIP || !m.network.pending {
		t.Fatal("returning to the network must still require IP confirmation")
	}
}
