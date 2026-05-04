package tftp

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	opRRQ  = 1
	opWRQ  = 2
	opDATA = 3
	opACK  = 4
	opERR  = 5
	opOACK = 6

	defaultBlockSize = 512
	maxBlockSize     = 1468 // Ethernet MTU (1500) - IP(20) - UDP(8) - TFTP header(4)
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
			filename, options := parseRRQ(buf[2:n])
			log.Printf("[TFTP] RRQ: %s from %s (options: %v)", filename, remote, options)
			go s.handleRead(filename, options, remote)
		}
	}
}

// parseRRQ parses filename, mode, and options from RRQ packet
func parseRRQ(data []byte) (string, map[string]string) {
	options := make(map[string]string)
	parts := splitNullTerminated(data)

	if len(parts) < 2 {
		return "", options
	}

	filename := parts[0]
	// parts[1] is the mode (octet/netascii) - we ignore it

	// Parse options (key-value pairs after mode)
	for i := 2; i+1 < len(parts); i += 2 {
		key := strings.ToLower(parts[i])
		value := parts[i+1]
		options[key] = value
	}

	return filename, options
}

func splitNullTerminated(data []byte) []string {
	var parts []string
	start := 0
	for i, b := range data {
		if b == 0 {
			parts = append(parts, string(data[start:i]))
			start = i + 1
		}
	}
	return parts
}

func (s *Server) handleRead(filename string, options map[string]string, remote *net.UDPAddr) {
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

	// Determine block size - negotiate if client requested it
	blkSize := defaultBlockSize
	var oackOptions []string

	if val, ok := options["blksize"]; ok {
		requested, err := strconv.Atoi(val)
		if err == nil && requested > 0 {
			if requested > maxBlockSize {
				requested = maxBlockSize
			}
			blkSize = requested
			oackOptions = append(oackOptions, "blksize", strconv.Itoa(blkSize))
		}
	}

	if _, ok := options["tsize"]; ok {
		oackOptions = append(oackOptions, "tsize", strconv.Itoa(len(data)))
	}

	// If client requested options, send OACK and wait for ACK 0
	if len(oackOptions) > 0 {
		oack := buildOACK(oackOptions)
		log.Printf("[TFTP] Sending OACK (blksize=%d, tsize=%d) to %s", blkSize, len(data), remote)

		acked := false
		for retries := 0; retries < 5; retries++ {
			conn.Write(oack)
			ackBuf := make([]byte, 4)
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			n, err := conn.Read(ackBuf)
			if err != nil {
				continue
			}
			if n >= 4 && binary.BigEndian.Uint16(ackBuf[:2]) == opACK {
				ackBlock := binary.BigEndian.Uint16(ackBuf[2:4])
				if ackBlock == 0 {
					acked = true
					break
				}
			}
		}
		if !acked {
			log.Printf("[TFTP] OACK not acknowledged by %s, aborting", remote)
			return
		}
	}

	// Send file data
	block := uint16(1)
	offset := 0

	for {
		end := offset + blkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]

		pkt := make([]byte, 4+len(chunk))
		binary.BigEndian.PutUint16(pkt[:2], opDATA)
		binary.BigEndian.PutUint16(pkt[2:4], block)
		copy(pkt[4:], chunk)

		acked := false
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
					acked = true
					break
				}
			}
		}

		if !acked {
			log.Printf("[TFTP] Transfer failed at block %d for %s", block, filename)
			return
		}

		if len(chunk) < blkSize {
			log.Printf("[TFTP] Transfer complete: %s (%d blocks, blksize=%d)", filename, block, blkSize)
			return
		}

		block++
		offset += blkSize
	}
}

func buildOACK(options []string) []byte {
	pkt := make([]byte, 2)
	binary.BigEndian.PutUint16(pkt[:2], opOACK)
	for _, opt := range options {
		pkt = append(pkt, []byte(opt)...)
		pkt = append(pkt, 0)
	}
	return pkt
}
