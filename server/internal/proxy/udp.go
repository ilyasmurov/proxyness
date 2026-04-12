package proxy

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
)

const udpTimeout = 60 * time.Second
const udpBufSize = 65535

func (h *Handler) handleUDP(conn net.Conn, device *db.Device, isTLS bool) {
	connID := h.Tracker.Add(device.ID, device.Name, device.UserName, device.Version, isTLS)
	var totalIn, totalOut int64
	var mu sync.Mutex

	udpConns := make(map[string]*net.UDPConn)
	var connsMu sync.Mutex

	defer func() {
		connsMu.Lock()
		for _, uc := range udpConns {
			uc.Close()
		}
		connsMu.Unlock()

		h.Tracker.Remove(connID)
		mu.Lock()
		recordTraffic(h.DB, device.ID, totalIn, totalOut)
		mu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			addr, port, payload, err := proto.ReadUDPFrame(conn)
			if err != nil {
				return
			}

			mu.Lock()
			totalOut += int64(len(payload))
			mu.Unlock()
			h.Tracker.AddBytes(connID, 0, int64(len(payload)))

			target := fmt.Sprintf("%s:%d", addr, port)

			connsMu.Lock()
			uc, exists := udpConns[target]
			if !exists {
				raddr, err := net.ResolveUDPAddr("udp", target)
				if err != nil {
					connsMu.Unlock()
					log.Printf("resolve udp %s: %v", target, err)
					continue
				}
				uc, err = net.DialUDP("udp", nil, raddr)
				if err != nil {
					connsMu.Unlock()
					log.Printf("dial udp %s: %v", target, err)
					continue
				}
				udpConns[target] = uc

				go func(uc *net.UDPConn, addr string, port uint16) {
					buf := make([]byte, udpBufSize)
					for {
						uc.SetReadDeadline(time.Now().Add(udpTimeout))
						n, err := uc.Read(buf)
						if err != nil {
							return
						}
						mu.Lock()
						totalIn += int64(n)
						mu.Unlock()
						h.Tracker.AddBytes(connID, int64(n), 0)

						if err := proto.WriteUDPFrame(conn, addr, port, buf[:n]); err != nil {
							return
						}
					}
				}(uc, addr, port)
			}
			connsMu.Unlock()

			uc.SetWriteDeadline(time.Now().Add(udpTimeout))
			if _, err := uc.Write(payload); err != nil {
				log.Printf("udp write to %s: %v", target, err)
			}
		}
	}()

	<-done
}
