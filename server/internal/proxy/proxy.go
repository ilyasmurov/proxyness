package proxy

import (
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"proxyness/pkg/auth"
	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
	"proxyness/server/internal/stats"
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

	// Read msg type — may be MsgTypeMachineID (new clients) or TCP/UDP (old clients)
	msgType, err := proto.ReadMsgType(conn)
	if err != nil {
		log.Printf("read msg type: %v", err)
		return
	}

	// New clients send machine ID before msg type
	if msgType == proto.MsgTypeMachineID {
		machineID, err := proto.ReadMachineID(conn)
		if err != nil {
			log.Printf("read machine id: %v", err)
			return
		}

		mid := hex.EncodeToString(machineID[:])
		if err := h.checkMachineID(device.ID, device.Name, mid); err != nil {
			log.Printf("device %s machine check failed: %v", device.Name, err)
			proto.WriteResult(conn, false)
			return
		}
		proto.WriteResult(conn, true)

		// Read actual msg type
		msgType, err = proto.ReadMsgType(conn)
		if err != nil {
			log.Printf("read msg type: %v", err)
			return
		}

		// Mux-capable clients (PRXNS-33) keep this connection as a yamux
		// session and run every stream inside it. Only offered after
		// machine-id, so the device binding is checked once per session.
		if msgType == proto.MsgTypeMux {
			proto.WriteResult(conn, true)
			start := time.Now()
			streams, err := serveMux(conn, h.dispatchStream(&device, isTLS), h.deviceStillAllowed(device.Name, matchedKey))
			log.Printf("device %s mux session from %s closed after %s, %d streams: %v",
				device.Name, conn.RemoteAddr(), time.Since(start).Round(time.Second), streams, err)
			return
		}
	}

	h.dispatchStream(&device, isTLS)(conn, msgType)
}

// deviceStillAllowed is the recheck for one mux session: false once the
// device behind matchedKey is disabled or deleted, true otherwise — including
// when the lookup fails for any other reason (see muxRevokeReason). The lookup
// goes through the device cache, so a revoke lands within about two intervals.
func (h *Handler) deviceStillAllowed(name, matchedKey string) func() bool {
	return func() bool {
		dev, err := h.DB.GetDeviceByKey(matchedKey)
		reason := muxRevokeReason(dev, err)
		if reason == "" {
			return true
		}
		log.Printf("device %s mux session closed: device %s", name, reason)
		return false
	}
}

// dispatchStream returns the handler for one stream that has already sent its
// msg type: a whole connection on the per-connection path, a yamux stream
// inside a mux session.
func (h *Handler) dispatchStream(device *db.Device, isTLS bool) func(net.Conn, byte) {
	return func(conn net.Conn, msgType byte) {
		switch msgType {
		case proto.MsgTypeTCP:
			h.handleTCP(conn, device, isTLS)
		case proto.MsgTypeUDP:
			h.handleUDP(conn, device, isTLS)
		default:
			log.Printf("unknown msg type: 0x%02x", msgType)
		}
	}
}

// checkMachineID validates or binds a machine fingerprint to a device.
// First connection: stores the fingerprint. Subsequent: checks match.
func (h *Handler) checkMachineID(deviceID int, deviceName, machineID string) error {
	stored, err := h.DB.GetDeviceMachineID(deviceID)
	if err != nil {
		return err
	}
	if stored == "" {
		// First time — bind this machine to the device
		log.Printf("device %s bound to machine %s", deviceName, machineID[:8])
		return h.DB.SetDeviceMachineID(deviceID, machineID)
	}
	if stored != machineID {
		return fmt.Errorf("bound to different machine")
	}
	return nil
}

func recordTraffic(database *db.DB, deviceID int, bytesIn, bytesOut int64) {
	hour := time.Now().Truncate(time.Hour)
	database.RecordTraffic(deviceID, hour, bytesIn, bytesOut, 1)
}
