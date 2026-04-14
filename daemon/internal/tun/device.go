package tun

import (
	"fmt"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const channelSize = 512

// newStack creates a gVisor network stack with a channel-based link endpoint.
func newStack(mtu uint32) (*stack.Stack, *channel.Endpoint, error) {
	ep := channel.New(channelSize, mtu, "")

	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
			ipv6.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol,
			udp.NewProtocol,
			icmp.NewProtocol4,
			icmp.NewProtocol6,
		},
	})

	// TCP buffer sizes for gVisor's TCP endpoints (the netstack acts as the
	// remote peer for every app-initiated connection intercepted off TUN).
	//
	// Max=1MB was previously capped at 64KB "to prevent memory blowup". With
	// a typical 20-50 active proxy flows, 1MB × 2 (send+recv) × 50 conns ≈
	// 100 MB absolute worst case — acceptable for a desktop client, much
	// less for short-lived bursts. Auto-tuning keeps each connection at a
	// fraction of the cap when idle.
	//
	// Why the previous 64KB hurt: end-to-end throughput per TCP session is
	// bounded by window/RTT. The download path is
	//   gVisor → channel.Endpoint → bridgeOutbound → helperWriteMu → helper
	//   → TUN device → kernel TCP → app
	// and the matching ACK path comes back through bridgeInbound. Even the
	// "local" RTT over this pipeline is tens of ms (helper IPC serialisation
	// + TUN write + kernel scheduling + return trip), so 64KB / 20ms capped
	// a single TCP flow at ~3 MB/s regardless of how fast the ARQ tunnel
	// could deliver bytes. Raising the cap lets one curl/apt/youtube-dl
	// flow actually saturate the tunnel.
	tcpRecvBuf := tcpip.TCPReceiveBufferSizeRangeOption{
		Min:     4096,
		Default: 131072,
		Max:     1 << 20, // 1 MiB
	}
	s.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpRecvBuf)
	tcpSendBuf := tcpip.TCPSendBufferSizeRangeOption{
		Min:     4096,
		Default: 131072,
		Max:     1 << 20, // 1 MiB
	}
	s.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpSendBuf)

	nicID := tcpip.NICID(1)
	if err := s.CreateNIC(nicID, ep); err != nil {
		return nil, nil, fmt.Errorf("create NIC: %v", err)
	}

	if err := s.SetPromiscuousMode(nicID, true); err != nil {
		return nil, nil, fmt.Errorf("set promiscuous mode: %v", err)
	}

	if err := s.SetSpoofing(nicID, true); err != nil {
		return nil, nil, fmt.Errorf("set spoofing: %v", err)
	}

	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})

	return s, ep, nil
}
