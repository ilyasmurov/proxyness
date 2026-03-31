//go:build darwin

package tun

/*
#include <libproc.h>
#include <sys/socket.h>
#include <netinet/in.h>

static uint16_t get_tcp_lport(struct socket_fdinfo *si) {
    return ntohs(si->psi.soi_proto.pri_tcp.tcpsi_ini.insi_lport);
}
static uint16_t get_udp_lport(struct socket_fdinfo *si) {
    return ntohs(si->psi.soi_proto.pri_in.insi_lport);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type darwinProcessInfo struct{}

func newProcessInfo() ProcessInfo {
	return &darwinProcessInfo{}
}

func (d *darwinProcessInfo) FindProcess(network string, localPort uint16) (string, error) {
	var wantProto C.int
	switch network {
	case "tcp":
		wantProto = C.IPPROTO_TCP
	case "udp":
		wantProto = C.IPPROTO_UDP
	default:
		return "", fmt.Errorf("unsupported network: %s", network)
	}

	n := C.proc_listpids(C.PROC_ALL_PIDS, 0, nil, 0)
	if n <= 0 {
		return "", nil
	}
	pids := make([]C.int, int(n)/4+16)
	n = C.proc_listpids(C.PROC_ALL_PIDS, 0, unsafe.Pointer(&pids[0]), n)
	if n <= 0 {
		return "", nil
	}
	pidCount := int(n) / 4

	fdInfoSize := C.int(unsafe.Sizeof(C.struct_proc_fdinfo{}))

	for i := 0; i < pidCount; i++ {
		pid := pids[i]
		if pid == 0 {
			continue
		}

		bufSize := C.proc_pidinfo(pid, C.PROC_PIDLISTFDS, 0, nil, 0)
		if bufSize <= 0 {
			continue
		}

		fds := make([]C.struct_proc_fdinfo, int(bufSize)/int(fdInfoSize))
		rn := C.proc_pidinfo(pid, C.PROC_PIDLISTFDS, 0, unsafe.Pointer(&fds[0]), bufSize)
		if rn <= 0 {
			continue
		}
		fdCount := int(rn) / int(fdInfoSize)

		for j := 0; j < fdCount; j++ {
			if fds[j].proc_fdtype != C.PROX_FDTYPE_SOCKET {
				continue
			}

			var si C.struct_socket_fdinfo
			sn := C.proc_pidfdinfo(pid, fds[j].proc_fd, C.PROC_PIDFDSOCKETINFO, unsafe.Pointer(&si), C.int(unsafe.Sizeof(si)))
			if sn <= 0 {
				continue
			}
			if si.psi.soi_family != C.AF_INET || si.psi.soi_protocol != wantProto {
				continue
			}

			var lport uint16
			if network == "tcp" {
				lport = uint16(C.get_tcp_lport(&si))
			} else {
				lport = uint16(C.get_udp_lport(&si))
			}

			if lport == localPort {
				buf := make([]C.char, 1024)
				pn := C.proc_pidpath(pid, unsafe.Pointer(&buf[0]), 1024)
				if pn > 0 {
					return C.GoString(&buf[0]), nil
				}
			}
		}
	}

	return "", nil
}
