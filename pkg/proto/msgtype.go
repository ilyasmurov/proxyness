package proto

const (
	MsgTypeTCP       = 0x01
	MsgTypeUDP       = 0x02
	MsgTypeMachineID = 0x03

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
