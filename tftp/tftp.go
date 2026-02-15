package tftp

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	opRRQ  = 1
	opWRQ  = 2
	opDATA = 3
	opACK  = 4
	opERR  = 5

	blockSize = 512
)

type Server struct {
	root string
}

func NewServer(root string) *Server {
	return &Server{root: root}
}

func (s *Server) ListenAndServe(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return fmt.Errorf("TFTP listen: %w", err)
	}
	defer conn.Close()

	log.Printf("[TFTP] Listening on %s, root: %s", addr, s.root)

	buf := make([]byte, 1500)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("[TFTP] Read error: %v", err)
			continue
		}

		if n < 4 {
			continue
		}

		opcode := binary.BigEndian.Uint16(buf[:2])
		if opcode == opRRQ {
			rest := buf[2:n]
			idx := 0
			for idx < len(rest) && rest[idx] != 0 {
				idx++
			}
			filename := string(rest[:idx])
			log.Printf("[TFTP] RRQ: %s from %s", filename, remote)
			go s.handleRead(filename, remote)
		}
	}
}

func (s *Server) handleRead(filename string, remote *net.UDPAddr) {
	clean := filepath.Clean(filename)
	clean = strings.TrimPrefix(clean, "/")
	if strings.Contains(clean, "..") {
		log.Printf("[TFTP] Rejected path traversal: %s", filename)
		return
	}

	fullPath := filepath.Join(s.root, clean)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		log.Printf("[TFTP] File not found: %s (%v)", fullPath, err)
		conn, err2 := net.DialUDP("udp4", nil, remote)
		if err2 != nil {
			return
		}
		defer conn.Close()
		errMsg := fmt.Sprintf("File not found: %s", filename)
		pkt := make([]byte, 5+len(errMsg))
		binary.BigEndian.PutUint16(pkt[:2], opERR)
		binary.BigEndian.PutUint16(pkt[2:4], 1)
		copy(pkt[4:], errMsg)
		conn.Write(pkt)
		return
	}

	log.Printf("[TFTP] Sending %s (%d bytes) to %s", filename, len(data), remote)

	conn, err := net.DialUDP("udp4", nil, remote)
	if err != nil {
		log.Printf("[TFTP] Dial error: %v", err)
		return
	}
	defer conn.Close()

	block := uint16(1)
	offset := 0

	for {
		end := offset + blockSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]

		pkt := make([]byte, 4+len(chunk))
		binary.BigEndian.PutUint16(pkt[:2], opDATA)
		binary.BigEndian.PutUint16(pkt[2:4], block)
		copy(pkt[4:], chunk)

		for retries := 0; retries < 5; retries++ {
			conn.Write(pkt)
			ackBuf := make([]byte, 4)
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			n, err := conn.Read(ackBuf)
			if err != nil {
				continue
			}
			if n >= 4 && binary.BigEndian.Uint16(ackBuf[:2]) == opACK {
				ackBlock := binary.BigEndian.Uint16(ackBuf[2:4])
				if ackBlock == block {
					break
				}
			}
		}

		if len(chunk) < blockSize {
			log.Printf("[TFTP] Transfer complete: %s", filename)
			return
		}

		block++
		offset = int(block-1) * blockSize
	}
}
