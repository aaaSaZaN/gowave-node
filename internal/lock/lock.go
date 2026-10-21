package lock

import (
	"errors"
	"log"
	"net"
	"syscall"
)

var lockListener net.Listener

// acquire instance lock
func AcquireInstanceLock() bool {
	l, err := net.Listen("unix", "\x00rwnode-lock")
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return false
		}
		// try filesystem fallback on non-linux
		l, err = net.Listen("unix", "/tmp/rwnode-lock.sock")
		if err != nil {
			return false
		}
	}

	lockListener = l
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	return true
}

func ReleaseInstanceLock() {
	if lockListener != nil {
		_ = lockListener.Close()
	}
}

func DuplicateInstanceMessage() string {
	return "[WARN] Another instance of gowave-node is already running!"
}

func PrintStartMessage(port int, xrayVersion string, cpus int, memTotal uint64) {
	log.Printf("[START] gowave-node started on port :%d (Xray: %s, %d CPUs)", port, xrayVersion, cpus)
}
