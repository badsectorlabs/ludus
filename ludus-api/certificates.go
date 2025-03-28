package ludusapi

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

func getCRLDomainsFromDomain(domain string) []string {
	conn, err := tls.Dial("tcp", domain+":443", nil)
	if err != nil {
		fmt.Println("Failed to connect to", domain)
		return nil
	}
	defer conn.Close()

	state := conn.ConnectionState()
	var allCRLDomains []string
	for _, chain := range state.PeerCertificates {
		// Parse the certificate
		block := &pem.Block{Type: "CERTIFICATE", Bytes: chain.Raw}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			fmt.Println("Failed to parse certificate:", err)
			continue
		}

		// Extract CRL URLs from the certificate
		crlDomains := extractCRLDomains(cert)
		for _, domain := range crlDomains {
			if !containsSubstring(allCRLDomains, domain) {
				allCRLDomains = append(allCRLDomains, domain)
			}
		}

		// Also allow the OCSP URLs in the "Authority Information Access" section
		for _, ocspURL := range cert.OCSPServer {
			u, err := url.Parse(ocspURL)
			if err != nil {
				fmt.Printf("Error parsing URL: %v\n", err)
				continue
			}

			// Extract the hostname (domain) from the URL
			domain := u.Hostname()
			if !containsSubstring(allCRLDomains, domain) {
				allCRLDomains = append(allCRLDomains, domain)
			}
		}
	}
	return allCRLDomains
}

func extractCRLDomains(cert *x509.Certificate) []string {
	var crlDomains []string

	for _, ext := range cert.CRLDistributionPoints {
		if strings.HasPrefix(ext, "http://") || strings.HasPrefix(ext, "https://") {
			u, err := url.Parse(ext)
			if err != nil {
				fmt.Printf("Error parsing URL: %v\n", err)
				continue
			}

			// Extract the hostname (domain) from the URL
			domain := u.Hostname()

			crlDomains = append(crlDomains, domain)
		}
	}

	return crlDomains
}

// return a single IPv4 address for a given domain
func GetIPFromDomain(domain string) (string, error) {
	ips, _ := net.LookupIP(domain)
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			return ipv4.String(), nil
		}
	}
	return "", errors.New("could not resolve IP for the domain " + domain)
}
