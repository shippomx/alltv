package newtvauth

import (
	"net/netip"
	"testing"
)

func TestChartIndexResolve(t *testing.T) {
	resources := ChartResources{
		Arg:   []byte(`{"arg_list":[{"proid":"1","chid":"00112233445566778899aabbccddeeff","name":"AAA","pps":400}]}`),
		Chart: []byte(`{"chart_list":[{"proid":"1","chid":"00112233445566778899aabbccddeeff","name":"AAA","ss_list":[{"addr":"127.0.0.1:9000","roomid":1},{"addr":"127.0.0.1:9000","roomid":1}]}]}`),
	}
	index, err := ParseChartIndex(resources)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := index.Resolve("1", "authorized", 42)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Channel[0] != 0 || channel.Channel[15] != 0xff || string(channel.DeviceID[:10]) != "authorized" || channel.ClientID != 42 || len(channel.Peers) != 1 {
		t.Fatalf("channel = %#v", channel)
	}
}

func TestChartIndexResolveRejectsMismatchedChannel(t *testing.T) {
	index := ChartIndex{Arguments: []ChannelArgument{{ProviderID: "1", ChannelID: "00112233445566778899aabbccddeeff"}}, Providers: []ChannelProviders{{ProviderID: "1", ChannelID: "ffeeddccbbaa99887766554433221100", Servers: []StreamServer{{Address: "127.0.0.1:1"}}}}}
	if _, err := index.Resolve("1", "device", 1); err == nil {
		t.Fatal("accepted mismatched chart entries")
	}
}

func TestChartIndexResolveSelectsAdjacentSortedServersAndMatchingHoles(t *testing.T) {
	index := ChartIndex{
		Arguments: []ChannelArgument{{ProviderID: "1", ChannelID: "00112233445566778899aabbccddeeff"}},
		Providers: []ChannelProviders{{ProviderID: "1", ChannelID: "00112233445566778899aabbccddeeff", Servers: []StreamServer{
			{Address: "192.0.2.1:9000", RoomID: 2}, {Address: "192.0.2.2:9000", RoomID: 2},
			{Address: "198.51.100.1:9000", RoomID: 1}, {Address: "198.51.100.2:9000", RoomID: 1},
		}}},
		Rules: []RoomFakeRule{
			{RoomID: 2, Air: []FakeAddressRule{{Network: "203.0.113.0", Mask: 24, Port1: 6000, Port2: 6001}}},
			{RoomID: 1, Air: []FakeAddressRule{{Network: "198.18.0.0", Mask: 24, Port1: 7000, Port2: 7001}}},
		},
	}
	channel, err := index.Resolve("1", "device", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(channel.Peers) != 2 || len(channel.HolePunchPeers) != 2 {
		t.Fatalf("peers=%v holes=%v", channel.Peers, channel.HolePunchPeers)
	}
	if len(channel.EndpointSets) != 4 {
		t.Fatalf("endpoint sets = %d, want 4", len(channel.EndpointSets))
	}
	order := map[string]int{"192.0.2.1:9000": 0, "192.0.2.2:9000": 1, "198.51.100.1:9000": 2, "198.51.100.2:9000": 3}
	for _, endpoints := range channel.EndpointSets {
		if (order[endpoints.Peers[0]]+1)%4 != order[endpoints.Peers[1]] {
			t.Fatalf("fallback peers are not circular neighbors: %v", endpoints.Peers)
		}
	}
	if (order[channel.Peers[0]]+1)%4 != order[channel.Peers[1]] {
		t.Fatalf("peers are not circular neighbors: %v", channel.Peers)
	}
	for i, peer := range channel.Peers {
		address, _ := netip.ParseAddrPort(channel.HolePunchPeers[i])
		want := netip.MustParsePrefix("203.0.113.0/24")
		if order[peer] >= 2 {
			want = netip.MustParsePrefix("198.18.0.0/24")
		}
		if !want.Contains(address.Addr()) {
			t.Fatalf("peer %q hole = %q", peer, channel.HolePunchPeers[i])
		}
	}
}

func TestChartIndexResolveAll(t *testing.T) {
	index := ChartIndex{
		Arguments: []ChannelArgument{
			{ProviderID: "1", ChannelID: "00112233445566778899aabbccddeeff", Name: "One"},
			{ProviderID: "2", ChannelID: "ffeeddccbbaa99887766554433221100", Name: "Two"},
			{ProviderID: "menu", ChannelID: "not-media", Name: "Menu"},
		},
		Providers: []ChannelProviders{
			{ProviderID: "1", ChannelID: "00112233445566778899aabbccddeeff", Servers: []StreamServer{{Address: "127.0.0.1:1", RoomID: 1}}},
			{ProviderID: "2", ChannelID: "ffeeddccbbaa99887766554433221100", Servers: []StreamServer{{Address: "127.0.0.1:2", RoomID: 1}}},
		},
	}
	channels, err := index.ResolveAll("device", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 || channels[0].ProviderID != "1" || channels[0].Name != "One" || channels[1].ProviderID != "2" {
		t.Fatalf("channels = %#v", channels)
	}
}

func TestFakeEndpointHonorsRule(t *testing.T) {
	for i := 0; i < 20; i++ {
		raw, err := fakeEndpoint(FakeAddressRule{Network: "192.0.2.0", Mask: 24, Port1: 6000, Port2: 6010})
		if err != nil {
			t.Fatal(err)
		}
		address, err := netip.ParseAddrPort(raw)
		if err != nil || !netip.MustParsePrefix("192.0.2.0/24").Contains(address.Addr()) || address.Port() < 6000 || address.Port() > 6010 {
			t.Fatalf("endpoint = %q, err=%v", raw, err)
		}
	}
}
