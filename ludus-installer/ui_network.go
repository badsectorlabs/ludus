package main

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type networkSuggestionState struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	request                 uint64
	pending                 bool
	bridge, vlan            string
	ip, gateway, status     string
	manualIP, manualGateway bool
}

type networkSuggestionMsg struct {
	request    uint64
	bridge     string
	vlan       string
	suggestion managementAddressSuggestion
	err        error
}

func (m *installerFormModel) initializeNetworkSuggestion() {
	m.network = networkSuggestionState{
		bridge: m.values[uiBridge], vlan: m.values[uiVLANTag],
		manualIP:      m.options.IP != "" || m.options.Provided["ip"],
		manualGateway: m.options.Gateway != "" || m.options.Provided["gw"],
	}
}

func (m *installerFormModel) cancelNetworkSuggestion() {
	if m.network.cancel != nil {
		m.network.cancel()
		m.network.cancel = nil
	}
	m.network.request++
	m.network.pending = false
}

func (m *installerFormModel) networkHint() {
	hint := installerUIFields[uiIP].hint
	if m.network.status != "" {
		hint += " " + terminalText(m.network.status)
	}
	m.inputs["ETH0_IP"].Description(hint)
}

func (m *installerFormModel) synchronizeNetworkSelection() {
	bridge, vlan := m.values[uiBridge], m.values[uiVLANTag]
	if bridge == m.network.bridge && vlan == m.network.vlan {
		return
	}
	m.cancelNetworkSuggestion()
	// Only discard values supplied by this suggestion. Explicit and edited
	// values belong to the operator, even when another network is selected.
	if m.network.ip != "" && m.values[uiIP] == m.network.ip && !m.network.manualIP {
		m.values[uiIP] = ""
		m.inputs["ETH0_IP"].Value(&m.values[uiIP])
	}
	if m.network.gateway != "" && m.values[uiGateway] == m.network.gateway && !m.network.manualGateway {
		m.values[uiGateway] = ""
		m.inputs["ETH0_GW"].Value(&m.values[uiGateway])
	}
	m.network.bridge, m.network.vlan = bridge, vlan
	m.network.ip, m.network.gateway, m.network.status = "", "", ""
	m.networkHint()
}

func (m *installerFormModel) observeNetworkEdits(previousIP, previousGateway string) {
	if m.values[uiIP] != previousIP {
		m.network.manualIP = true
		m.network.ip = ""
		m.cancelNetworkSuggestion()
		m.network.status = ""
		m.networkHint()
	}
	if m.values[uiGateway] != previousGateway {
		m.network.manualGateway = true
		m.network.gateway = ""
		// A probe begun with a different gateway cannot exclude the new one.
		if m.network.pending {
			m.cancelNetworkSuggestion()
			m.network.status = "Gateway changed; enter an IP or return to the bridge or VLAN field to request a new suggestion."
			m.networkHint()
		}
	}
}

func (m *installerFormModel) startNetworkSuggestion() tea.Cmd {
	if m.options.MigrateHost || m.options.NoPrompt || m.network.manualIP || m.values[uiIP] != "" {
		return nil
	}
	// synchronizeNetworkSelection cancels a pending probe whenever the bridge
	// or VLAN changes, so a pending probe here is for the current network.
	if m.network.pending {
		return nil
	}
	m.cancelNetworkSuggestion()
	base := m.network.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	m.network.cancel, m.network.pending = cancel, true
	m.network.status = "Checking candidate addresses on this bridge/VLAN; you can enter an address instead."
	m.networkHint()
	request, bridge, vlan := m.network.request, m.values[uiBridge], m.values[uiVLANTag]
	inv, gateway := m.inventory, m.values[uiGateway]
	return func() tea.Msg {
		defer cancel()
		suggestion, err := suggestManagementAddress(ctx, inv, bridge, vlan, gateway)
		return networkSuggestionMsg{request: request, bridge: bridge, vlan: vlan, suggestion: suggestion, err: err}
	}
}

func (m *installerFormModel) acceptNetworkSuggestion(msg networkSuggestionMsg) {
	if !m.network.pending || msg.request != m.network.request || msg.bridge != m.values[uiBridge] || msg.vlan != m.values[uiVLANTag] {
		return
	}
	m.cancelNetworkSuggestion()
	if m.network.manualIP || m.values[uiIP] != "" {
		return
	}
	if msg.err != nil {
		m.network.status = fmt.Sprintf("No suggestion: %v. Enter a static address yourself.", msg.err)
		m.networkHint()
		return
	}
	m.values[uiIP], m.network.ip = msg.suggestion.IP, msg.suggestion.IP
	m.inputs["ETH0_IP"].Value(&m.values[uiIP])
	if !m.network.manualGateway && m.values[uiGateway] == "" && msg.suggestion.Gateway != "" {
		m.values[uiGateway], m.network.gateway = msg.suggestion.Gateway, msg.suggestion.Gateway
		m.inputs["ETH0_GW"].Value(&m.values[uiGateway])
	}
	m.network.status = "Suggested IP below. Verify and reserve it outside DHCP before continuing."
	m.networkHint()
}
