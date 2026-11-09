package proto

const (
	MsgTypeTCP       = 0x01
	MsgTypeUDP       = 0x02
	MsgTypeMachineID = 0x03
	// MsgTypeMux follows a successful machine-id exchange and asks the server
	// to run a yamux session over the rest of the connection: every yamux
	// stream then starts with MsgTypeTCP/MsgTypeUDP and carries exactly what a
	// per-connection stream carries after machine-id. A server that answers
	// ResultOK supports it; a pre-mux server logs "unknown msg type" and closes
	// the connection, which tells the client to stay per-connection (PRXNS-33).
	MsgTypeMux = 0x04

	MachineIDLen = 16
)

// WriteMsgType sends a 1-byte message type.
func WriteMsgType(conn interface{ Write([]byte) (int, error) }, t byte) error {
	_, err := conn.Write([]byte{t})
	return err
}

// ReadMsgType reads a 1-byte message type.
func ReadMsgType(conn interface{ Read([]byte) (int, error) }) (byte, error) {
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	return buf[0], err
}
