package protocol_test

import (
	"errors"
	"net"
	"net/rpc"
	"testing"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

type licenseHost struct{}

func (licenseHost) License(args protocol.CallArgs, reply *protocol.LicenseReply) error {
	switch args.Call {
	case "denied":
		reply.Err = protocol.HostErrorf(protocol.CodeForbidden, "not allowed")
	case "broken":
		return errors.New("host failure")
	default:
		reply.License = protocol.License{Valid: true, Name: string(args.Call)}
	}
	return nil
}

func TestHostClientSeparatesHostDecisionsFromTransportErrors(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName(protocol.HostServiceName, licenseHost{}); err != nil {
		t.Fatal(err)
	}
	hostConn, pluginConn := net.Pipe()
	go server.ServeConn(hostConn)
	client := protocol.NewHostClient(rpc.NewClient(pluginConn))
	defer client.Close()

	license, err := client.License("call-1")
	if err != nil || !license.Valid || license.Name != "call-1" {
		t.Fatalf("License(call-1) = %+v, %v", license, err)
	}

	_, err = client.License("denied")
	var hostErr *protocol.HostError
	if !errors.As(err, &hostErr) || hostErr.Code != protocol.CodeForbidden || hostErr.Message != "not allowed" {
		t.Fatalf("License(denied) error = %#v, want forbidden HostError", err)
	}
	if !errors.Is(err, &protocol.HostError{Code: protocol.CodeForbidden}) || errors.Is(err, &protocol.HostError{Code: protocol.CodeNotFound}) {
		t.Fatalf("errors.Is does not match HostError codes: %v", err)
	}

	for name, call := range map[string]func() error{
		"service error":  func() error { _, err := client.License("broken"); return err },
		"unknown method": func() error { _, err := client.RangeVMs("call-1", 1); return err },
	} {
		err := call()
		if err == nil || errors.As(err, &hostErr) {
			t.Errorf("%s: error = %#v, want a transport error", name, err)
		}
	}
}
