package ludusapi

import "testing"

func TestRangeVNetOptionsForZone(t *testing.T) {
	tag, vlanaware := RangeVNetOptionsForZone(SDNZoneTypeSimple, 4000, 7)
	if tag != 0 || !vlanaware {
		t.Fatalf("simple zone options: tag=%d vlanaware=%t", tag, vlanaware)
	}

	tag, vlanaware = RangeVNetOptionsForZone(SDNZoneTypeVXLAN, 4000, 7)
	if tag != 4007 || !vlanaware {
		t.Fatalf("vxlan zone options: tag=%d vlanaware=%t", tag, vlanaware)
	}
}

func TestNATVNetOptionsForZone(t *testing.T) {
	tag, vlanaware := NATVNetOptionsForZone(SDNZoneTypeSimple)
	if tag != 0 || vlanaware {
		t.Fatalf("simple NAT vnet options: tag=%d vlanaware=%t", tag, vlanaware)
	}

	tag, vlanaware = NATVNetOptionsForZone(SDNZoneTypeVXLAN)
	if tag != NATVNetVXLANTag || vlanaware {
		t.Fatalf("vxlan NAT vnet options: tag=%d vlanaware=%t", tag, vlanaware)
	}
}
