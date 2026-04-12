package proxy

import (
	"fmt"
	"log"
	"net"
	"time"

	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
)

func (h *Handler) handleTCP(conn net.Conn, device *db.Device, isTLS bool) {
	destAddr, port, err := proto.ReadConnect(conn)
	if err != nil {
		log.Printf("connect read: %v", err)
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", destAddr, port), 10*time.Second)
	if err != nil {
		proto.WriteResult(conn, false)
		return
	}
	defer target.Close()
	proto.WriteResult(conn, true)

	connID := h.Tracker.Add(device.ID, device.Name, device.UserName, device.Version, isTLS)
	proto.CountingRelay(conn, target, func(in, out int64) {
		h.Tracker.AddBytes(connID, in, out)
	})

	info := h.Tracker.Remove(connID)
	if info != nil {
		recordTraffic(h.DB, device.ID, info.BytesIn, info.BytesOut)
	}
}
