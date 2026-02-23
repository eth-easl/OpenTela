package server

import (
	"net"
	"opentela/internal/protocol"

	gostream "github.com/libp2p/go-libp2p-gostream"
	p2phttp "github.com/libp2p/go-libp2p-http"
)

func P2PListener() net.Listener {
	host, _ := protocol.GetP2PNode(nil)
	protocol.MarkSelfAsBootstrap()
	listener, _ := gostream.Listen(host, p2phttp.DefaultP2PProtocol)
	return listener
}
