package ludusapi

import (
	"bytes"
	"fmt"
	"ludusapi/models"
	"os"
	"strings"
	"text/template"
)

const userWireGuardConfigTemplate = `[Interface]
PrivateKey = {{ .client_private_key }}
Address = 198.51.100.{{ .user_number }}/32

[Peer]
PublicKey = {{ .server_public_key }}
Endpoint = {{ .proxmox_public_ip }}:{{ .wireguard_port}}
AllowedIPs = {{ .allowed_ips }}
PersistentKeepalive = 25
`

func getWireGuardConfigForUser(user *models.User) (string, error) {
	// Get the server public key
	serverPublicKey, err := os.ReadFile("/etc/wireguard/server-public-key")
	if err != nil {
		return "", err
	}

	// Get the client private key
	clientPrivateKey, err := os.ReadFile(fmt.Sprintf("/etc/wireguard/%s-client-private-key", user.UserId()))
	if err != nil {
		return "", err
	}

	// Render the template
	template, err := template.New("userWireGuardConfigTemplate").Parse(userWireGuardConfigTemplate)
	if err != nil {
		return "", err
	}

	allowedIPs, err := createAllowedIPsStringForUser(user)
	if err != nil {
		return "", err
	}

	var result bytes.Buffer
	if err := template.Execute(&result, map[string]interface{}{
		"client_private_key": strings.TrimSpace(string(clientPrivateKey)),
		"server_public_key":  strings.TrimSpace(string(serverPublicKey)),
		"proxmox_public_ip":  ServerConfiguration.ProxmoxPublicIP,
		"user_number":        user.UserNumber(),
		"allowed_ips":        allowedIPs,
		"wireguard_port":     ServerConfiguration.WireguardPort,
	}); err != nil {
		return "", err
	}

	return result.String(), nil
}

func createAllowedIPsStringForUser(user *models.User) (string, error) {
	elements := []string{
		"198.51.100.1/32", // Allow access to the server
	}

	accessibleRanges, err := GetAccessibleRangesForUser(user)
	if err != nil {
		return "", err
	}

	for _, rangeAccessibleObject := range accessibleRanges {
		elements = append(elements, fmt.Sprintf("10.%d.0.0/16", rangeAccessibleObject.RangeNumber))
	}

	return strings.Join(elements, ","), nil
}
