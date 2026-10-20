package ludus

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// APIClient returns an HTTP client for the local Ludus API described by
// env.API. The endpoint must be on the loopback interface. For https the
// client accepts only the exact certificate whose SHA-256 the host provided.
func APIClient(api protocol.APIEndpoint) (*http.Client, error) {
	baseURL, err := url.Parse(api.URL)
	if err != nil {
		return nil, fmt.Errorf("parse Ludus API URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, errors.New("Ludus API URL must use http or https")
	}
	if baseURL.Host == "" || !isLoopbackHost(baseURL.Hostname()) {
		return nil, errors.New("Ludus API URL must target the local host")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if baseURL.Scheme == "https" {
		fingerprint, err := hex.DecodeString(api.CertificateSHA256)
		if err != nil || len(fingerprint) != sha256.Size {
			return nil, errors.New("Ludus API certificate SHA-256 fingerprint is invalid")
		}
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // Identity is checked against the exact host-provided certificate below.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("Ludus API TLS peer did not provide a certificate")
				}
				actual := sha256.Sum256(state.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(actual[:], fingerprint) != 1 {
					return errors.New("Ludus API TLS certificate fingerprint mismatch")
				}
				return nil
			},
		}
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
