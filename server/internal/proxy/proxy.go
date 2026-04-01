package proxy

import (
	"io"
	"log"
	"net"
	"time"

	"smurov-proxy/pkg/auth"
	"smurov-proxy/pkg/proto"
	"smurov-proxy/server/internal/db"
	"smurov-proxy/server/internal/stats"
)

type Handler struct {
	DB      *db.DB
	Tracker *stats.Tracker
}

func (h *Handler) Handle(conn net.Conn, isTLS bool) {
	defer conn.Close()

	keys, err := h.DB.GetActiveKeys()
	if err != nil || len(keys) == 0 {
		log.Printf("no active keys: %v", err)
		return
	}

	msg := make([]byte, auth.AuthMsgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return
	}
	matchedKey, err := auth.ValidateAuthMessageMulti(keys, msg)
	if err != nil {
		proto.WriteResult(conn, false)
		log.Printf("auth failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	proto.WriteResult(conn, true)

	device, err := h.DB.GetDeviceByKey(matchedKey)
	if err != nil {
		log.Printf("device lookup: %v", err)
		return
	}

	// Lock device to this client IP — prevents same key on two machines
	remoteIP, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	if err := h.Tracker.LockDevice(device.ID, remoteIP); err != nil {
		log.Printf("device %s locked: %v (from %s)", device.Name, err, remoteIP)
		return
	}

	msgType, err := proto.ReadMsgType(conn)
	if err != nil {
		log.Printf("read msg type: %v", err)
		return
	}

	switch msgType {
	case proto.MsgTypeTCP:
		h.handleTCP(conn, &device, isTLS)
	case proto.MsgTypeUDP:
		h.handleUDP(conn, &device, isTLS)
	default:
		log.Printf("unknown msg type: 0x%02x", msgType)
	}
}

func recordTraffic(database *db.DB, deviceID int, bytesIn, bytesOut int64) {
	hour := time.Now().Truncate(time.Hour)
	database.RecordTraffic(deviceID, hour, bytesIn, bytesOut, 1)
}
