package newtvauth

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"sort"
)

type ChannelArgument struct {
	ProviderID string `json:"proid"`
	ChannelID  string `json:"chid"`
	Name       string `json:"name"`
	PPS        int    `json:"pps"`
	MinDelay   int    `json:"min_delay"`
	MaxDelay   int    `json:"max_delay"`
	ReportRate int    `json:"rpt_rate"`
	TempRate   int    `json:"temp_rate"`
	Single     int    `json:"single"`
}

type StreamServer struct {
	Address string `json:"addr"`
	RoomID  int    `json:"roomid"`
}

type ChannelProviders struct {
	ProviderID string         `json:"proid"`
	ChannelID  string         `json:"chid"`
	Name       string         `json:"name"`
	Servers    []StreamServer `json:"ss_list"`
}

type ChartIndex struct {
	Arguments []ChannelArgument
	Providers []ChannelProviders
	Rules     []RoomFakeRule
}

type FakeAddressRule struct {
	Network string `json:"network_addr"`
	Mask    int    `json:"mask"`
	Port1   int    `json:"port1"`
	Port2   int    `json:"port2"`
}

type RoomFakeRule struct {
	RoomID int               `json:"roomid"`
	Full   bool              `json:"fullfake"`
	Air    []FakeAddressRule `json:"rule_air"`
	HB     []FakeAddressRule `json:"rule_hb"`
}

type AuthorizedChannel struct {
	ProviderID     string
	Name           string
	Channel        [16]byte
	DeviceID       [16]byte
	ClientID       uint32
	Peers          []string
	HolePunchPeers []string
	EndpointSets   []AuthorizedEndpointSet
}

type AuthorizedEndpointSet struct {
	Peers          []string
	HolePunchPeers []string
}

func ParseChartIndex(resources ChartResources) (ChartIndex, error) {
	var arg struct {
		Arguments []ChannelArgument `json:"arg_list"`
		Rules     []RoomFakeRule    `json:"room_fake_rule"`
	}
	var chart struct {
		Providers []ChannelProviders `json:"chart_list"`
	}
	if err := json.Unmarshal(resources.Arg, &arg); err != nil {
		return ChartIndex{}, fmt.Errorf("decode NewTV channel arguments: %w", err)
	}
	if err := json.Unmarshal(resources.Chart, &chart); err != nil {
		return ChartIndex{}, fmt.Errorf("decode NewTV provider chart: %w", err)
	}
	if len(arg.Arguments) == 0 || len(chart.Providers) == 0 {
		return ChartIndex{}, fmt.Errorf("NewTV chart contains no channels")
	}
	return ChartIndex{Arguments: arg.Arguments, Providers: chart.Providers, Rules: arg.Rules}, nil
}

func (index ChartIndex) Resolve(providerID, device string, clientID uint32) (AuthorizedChannel, error) {
	if providerID == "" || device == "" || len(device) > 16 || clientID == 0 {
		return AuthorizedChannel{}, fmt.Errorf("incomplete authorized NewTV channel identity")
	}
	var argument *ChannelArgument
	for i := range index.Arguments {
		if index.Arguments[i].ProviderID == providerID {
			argument = &index.Arguments[i]
			break
		}
	}
	var providers *ChannelProviders
	for i := range index.Providers {
		if index.Providers[i].ProviderID == providerID {
			providers = &index.Providers[i]
			break
		}
	}
	if argument == nil || providers == nil || argument.ChannelID != providers.ChannelID {
		return AuthorizedChannel{}, fmt.Errorf("NewTV provider %q is missing or inconsistent", providerID)
	}
	channel, err := hex.DecodeString(argument.ChannelID)
	if err != nil || len(channel) != 16 {
		return AuthorizedChannel{}, fmt.Errorf("NewTV provider %q has invalid channel ID", providerID)
	}
	result := AuthorizedChannel{ProviderID: providerID, Name: argument.Name, ClientID: clientID}
	copy(result.Channel[:], channel)
	copy(result.DeviceID[:], device)
	type providerEndpoint struct {
		StreamServer
		key uint64
	}
	var servers []providerEndpoint
	seen := make(map[uint64]bool)
	for _, server := range providers.Servers {
		peer, err := net.ResolveUDPAddr("udp", server.Address)
		if err != nil {
			return AuthorizedChannel{}, fmt.Errorf("NewTV provider %q server %q: %w", providerID, server.Address, err)
		}
		ip := peer.IP.To4()
		if ip == nil || peer.Port <= 0 || peer.Port > 65535 {
			return AuthorizedChannel{}, fmt.Errorf("NewTV provider %q server %q is not IPv4 UDP", providerID, server.Address)
		}
		key := uint64(binary.BigEndian.Uint32(ip))<<32 | uint64(uint32(peer.Port))
		if !seen[key] {
			seen[key] = true
			servers = append(servers, providerEndpoint{StreamServer: server, key: key})
		}
	}
	if len(servers) == 0 {
		return AuthorizedChannel{}, fmt.Errorf("NewTV provider %q has no stream servers", providerID)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].key < servers[j].key })
	var selector [4]byte
	if _, err := rand.Read(selector[:]); err != nil {
		return AuthorizedChannel{}, err
	}
	start := int(binary.BigEndian.Uint32(selector[:]) % uint32(len(servers)))
	for offset := range servers {
		var endpoints AuthorizedEndpointSet
		for i := 0; i < min(2, len(servers)); i++ {
			server := servers[(start+offset+i)%len(servers)].StreamServer
			endpoints.Peers = append(endpoints.Peers, server.Address)
			for _, rule := range index.Rules {
				if rule.RoomID != server.RoomID || len(rule.Air) == 0 {
					continue
				}
				if _, err := rand.Read(selector[:]); err != nil {
					return AuthorizedChannel{}, err
				}
				address, err := fakeEndpoint(rule.Air[binary.BigEndian.Uint32(selector[:])%uint32(len(rule.Air))])
				if err != nil {
					return AuthorizedChannel{}, err
				}
				endpoints.HolePunchPeers = append(endpoints.HolePunchPeers, address)
				break
			}
		}
		result.EndpointSets = append(result.EndpointSets, endpoints)
	}
	result.Peers = result.EndpointSets[0].Peers
	result.HolePunchPeers = result.EndpointSets[0].HolePunchPeers
	return result, nil
}

func (index ChartIndex) ResolveAll(device string, clientID uint32) ([]AuthorizedChannel, error) {
	channels := make([]AuthorizedChannel, 0, len(index.Arguments))
	for _, argument := range index.Arguments {
		channel, err := index.Resolve(argument.ProviderID, device, clientID)
		if err != nil {
			continue
		}
		channels = append(channels, channel)
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("NewTV chart contains no playable channels")
	}
	return channels, nil
}

func fakeEndpoint(rule FakeAddressRule) (string, error) {
	address, err := netip.ParseAddr(rule.Network)
	if err != nil || !address.Is4() || rule.Mask < 8 || rule.Mask > 32 || rule.Port1 <= 0 || rule.Port2 < rule.Port1 || rule.Port2 > 65535 {
		return "", fmt.Errorf("invalid NewTV fake address rule")
	}
	prefix := netip.PrefixFrom(address, rule.Mask).Masked()
	base := binary.BigEndian.Uint32(prefix.Addr().AsSlice())
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate NewTV fake endpoint: %w", err)
	}
	if hostBits := 32 - rule.Mask; hostBits > 1 {
		hostCount := uint32(1)<<hostBits - 2
		base |= binary.BigEndian.Uint32(random[:4])%hostCount + 1
	}
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], base)
	port := rule.Port1
	if rule.Port2 > rule.Port1 {
		port += int(binary.BigEndian.Uint32(random[4:]) % uint32(rule.Port2-rule.Port1))
	}
	return net.JoinHostPort(net.IP(raw[:]).String(), fmt.Sprint(port)), nil
}
