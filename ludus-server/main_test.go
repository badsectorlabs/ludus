package main

import (
	"testing"

	ludusapi "ludusapi"
)

func TestPluginAPIPortMatchesServerProcess(t *testing.T) {
	configuration := ludusapi.Configuration{
		Port:      8080,
		AdminPort: 8081,
	}
	tests := []struct {
		name string
		euid int
		want int
	}{
		{name: "main service", euid: 1000, want: configuration.Port},
		{name: "admin service", euid: 0, want: configuration.AdminPort},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pluginAPIPort(test.euid, configuration); got != test.want {
				t.Fatalf("pluginAPIPort(%d) = %d, want %d", test.euid, got, test.want)
			}
		})
	}
}
