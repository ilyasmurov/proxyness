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

	// Cap TCP buffer sizes to prevent gVisor auto-tuning from growing
	// to 4MB per connection. For a relay/proxy, 128KB max is plenty.
	// Without this, 500 connections × 8MB = 4GB RAM on Windows.
	tcpRecvBuf := tcpip.TCPReceiveBufferSizeRangeOption{
		Min:     4096,
		Default: 32768,
		Max:     131072,
	}
	s.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpRecvBuf)
	tcpSendBuf := tcpip.TCPSendBufferSizeRangeOption{
		Min:     4096,
		Default: 32768,
		Max:     131072,
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
