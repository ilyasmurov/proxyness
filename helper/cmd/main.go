package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"runtime"
)

type Request struct {
	Action string `json:"action"` // "create" or "destroy"
}

type Response struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	TUNName string `json:"tun_name,omitempty"`
}

func main() {
	log.SetPrefix("[helper] ")
	log.Printf("starting on %s/%s", runtime.GOOS, runtime.GOARCH)

	ln, err := listenIPC()
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	log.Printf("listening for connections")
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResponse(conn, Response{Error: fmt.Sprintf("decode: %v", err)})
		return
	}

	log.Printf("request: %s", req.Action)

	switch req.Action {
	case "create":
		resp := createTUN()
		writeResponse(conn, resp)
	case "destroy":
		resp := destroyTUN()
		writeResponse(conn, resp)
	default:
		writeResponse(conn, Response{Error: fmt.Sprintf("unknown action: %s", req.Action)})
	}
}

func writeResponse(conn net.Conn, resp Response) {
	if resp.Error != "" {
		resp.OK = false
	} else {
		resp.OK = true
	}
	json.NewEncoder(conn).Encode(resp)
}
