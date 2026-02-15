package dhcp

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
)

// DHCP message types
const (
	DISCOVER = 1
	OFFER    = 2
	REQUEST  = 3
	ACK      = 5
	NAK      = 6
)

// DHCP options
const (
	OptSubnetMask   = 1
	OptRouter       = 3
	OptDNS          = 6
	OptBroadcast    = 28
	OptRequestedIP  = 50
	OptLeaseTime    = 51
	OptMessageType  = 53
	OptServerID     = 54
	OptTFTPServer   = 66
	OptBootFile     = 67
	OptClientArch   = 93
	OptEnd          = 255
)

// Packet represents a BOOTP/DHCP packet
type Packet struct {
	Op      byte
	HType   byte
	HLen    byte
	Hops    byte
	XID     uint32
	Secs    uint16
	Flags   uint16
	CIAddr  net.IP
	YIAddr  net.IP
	SIAddr  net.IP
	GIAddr  net.IP
	CHAddr  net.HardwareAddr
	SName   [64]byte
	File    [128]byte
	Options map[byte][]byte
}

// Config holds DHCP server configuration
type Config struct {
	Interface  string
	ServerIP   net.IP
	RangeStart net.IP
	RangeEnd   net.IP
	SubnetMask net.IPMask
	BootFile   string
	TFTPServer string
}

type lease struct {
	IP  net.IP
	MAC net.HardwareAddr
}

// Server is a minimal DHCP server for PXE booting
type Server struct {
	config Config
	leases map[string]lease
	nextIP net.IP
	mu     sync.Mutex
}

// NewServer creates a new DHCP server
func NewServer(cfg Config) *Server {
	return &Server{
		config: cfg,
		leases: make(map[string]lease),
		nextIP: dupIP(cfg.RangeStart),
	}
}

func dupIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

func (s *Server) allocateIP(mac net.HardwareAddr) net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()

	macStr := mac.String()
	if l, ok := s.leases[macStr]; ok {
		return l.IP
	}

	ip := dupIP(s.nextIP)
	s.leases[macStr] = lease{IP: ip, MAC: mac}

	ipv4 := s.nextIP.To4()
	val := binary.BigEndian.Uint32(ipv4)
	val++
	binary.BigEndian.PutUint32(ipv4, val)
	s.nextIP = ipv4

	return ip
}

// ListenAndServe starts the DHCP server on port 67
func (s *Server) ListenAndServe() error {
	// Bind to the specific interface address for reliable broadcast on macOS
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 67}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("DHCP listen: %w", err)
	}
	defer conn.Close()

	log.Printf("[DHCP] Listening on :67 (interface %s, server %s)", s.config.Interface, s.config.ServerIP)

	buf := make([]byte, 1500)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("[DHCP] Read error: %v", err)
			continue
		}

		pkt, err := parsePacket(buf[:n])
		if err != nil {
			log.Printf("[DHCP] Parse error: %v", err)
			continue
		}

		msgType := pkt.Options[OptMessageType]
		if len(msgType) == 0 {
			continue
		}

		switch msgType[0] {
		case DISCOVER:
			log.Printf("[DHCP] DISCOVER from %s", pkt.CHAddr)
			s.sendOffer(conn, pkt, remote)
		case REQUEST:
			log.Printf("[DHCP] REQUEST from %s", pkt.CHAddr)
			s.sendACK(conn, pkt, remote)
		default:
			log.Printf("[DHCP] Type %d from %s", msgType[0], pkt.CHAddr)
		}
	}
}

func (s *Server) sendOffer(conn *net.UDPConn, req *Packet, remote *net.UDPAddr) {
	ip := s.allocateIP(req.CHAddr)
	log.Printf("[DHCP] OFFER %s -> %s", ip, req.CHAddr)
	s.sendReply(conn, req, OFFER, ip)
}

func (s *Server) sendACK(conn *net.UDPConn, req *Packet, remote *net.UDPAddr) {
	ip := s.allocateIP(req.CHAddr)
	log.Printf("[DHCP] ACK %s -> %s", ip, req.CHAddr)
	s.sendReply(conn, req, ACK, ip)
}

func (s *Server) sendReply(conn *net.UDPConn, req *Packet, msgType byte, clientIP net.IP) {
	// Determine boot file based on client architecture
	bootFile := s.config.BootFile
	if archOpt, ok := req.Options[OptClientArch]; ok && len(archOpt) >= 2 {
		arch := binary.BigEndian.Uint16(archOpt)
		if arch == 7 || arch == 9 {
			log.Printf("[DHCP] Client is UEFI (arch=%d), boot file: %s", arch, bootFile)
		}
	}

	reply := &Packet{
		Op:     2, // BOOTREPLY
		HType:  1,
		HLen:   6,
		XID:    req.XID,
		Flags:  req.Flags,
		YIAddr: clientIP.To4(),
		SIAddr: s.config.ServerIP.To4(),
		CHAddr: req.CHAddr,
		Options: map[byte][]byte{
			OptMessageType: {msgType},
			OptServerID:    s.config.ServerIP.To4(),
			OptSubnetMask:  net.IP(s.config.SubnetMask).To4(),
			OptRouter:      s.config.ServerIP.To4(),
			OptDNS:         s.config.ServerIP.To4(),
			OptLeaseTime:   {0, 0, 0x0E, 0x10}, // 3600 seconds
			OptBootFile:    []byte(bootFile),
			OptTFTPServer:  []byte(s.config.TFTPServer),
		},
	}

	// Set boot file in packet header fields (some PXE clients read these instead of options)
	copy(reply.File[:], bootFile)
	copy(reply.SName[:], s.config.TFTPServer)

	// Compute broadcast address
	subnet := make(net.IP, 4)
	serverIP := s.config.ServerIP.To4()
	mask := s.config.SubnetMask
	for i := 0; i < 4; i++ {
		subnet[i] = serverIP[i] | ^mask[i]
	}
	reply.Options[OptBroadcast] = subnet

	data := serializePacket(reply)

	// Send as broadcast on port 68
	// Use the broadcast flag from client, or always broadcast for PXE
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: 68}

	// On macOS, sending to 255.255.255.255 may not work on all interfaces.
	// Use the subnet broadcast address instead for reliability.
	subnetBcast := &net.UDPAddr{IP: subnet, Port: 68}
	if _, err := conn.WriteToUDP(data, subnetBcast); err != nil {
		// Fallback to global broadcast
		log.Printf("[DHCP] Subnet broadcast failed (%v), trying global broadcast", err)
		if _, err := conn.WriteToUDP(data, dst); err != nil {
			log.Printf("[DHCP] Send error: %v", err)
		}
	}
}

func parsePacket(data []byte) (*Packet, error) {
	if len(data) < 240 {
		return nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}

	p := &Packet{
		Op:      data[0],
		HType:   data[1],
		HLen:    data[2],
		Hops:    data[3],
		XID:     binary.BigEndian.Uint32(data[4:8]),
		Secs:    binary.BigEndian.Uint16(data[8:10]),
		Flags:   binary.BigEndian.Uint16(data[10:12]),
		CIAddr:  net.IP(make([]byte, 4)),
		YIAddr:  net.IP(make([]byte, 4)),
		SIAddr:  net.IP(make([]byte, 4)),
		GIAddr:  net.IP(make([]byte, 4)),
		CHAddr:  net.HardwareAddr(make([]byte, 6)),
		Options: make(map[byte][]byte),
	}
	copy(p.CIAddr, data[12:16])
	copy(p.YIAddr, data[16:20])
	copy(p.SIAddr, data[20:24])
	copy(p.GIAddr, data[24:28])
	copy(p.CHAddr, data[28:34])
	copy(p.SName[:], data[44:108])
	copy(p.File[:], data[108:236])

	// Parse options after magic cookie (99.130.83.99)
	if len(data) > 240 && data[236] == 99 && data[237] == 130 && data[238] == 83 && data[239] == 99 {
		i := 240
		for i < len(data) {
			opt := data[i]
			if opt == OptEnd {
				break
			}
			if opt == 0 {
				i++
				continue
			}
			if i+1 >= len(data) {
				break
			}
			length := int(data[i+1])
			if i+2+length > len(data) {
				break
			}
			optData := make([]byte, length)
			copy(optData, data[i+2:i+2+length])
			p.Options[opt] = optData
			i += 2 + length
		}
	}

	return p, nil
}

func serializePacket(p *Packet) []byte {
	buf := make([]byte, 576)

	buf[0] = p.Op
	buf[1] = p.HType
	buf[2] = p.HLen
	buf[3] = p.Hops
	binary.BigEndian.PutUint32(buf[4:8], p.XID)
	binary.BigEndian.PutUint16(buf[8:10], p.Secs)
	binary.BigEndian.PutUint16(buf[10:12], p.Flags)

	if p.CIAddr != nil {
		copy(buf[12:16], p.CIAddr.To4())
	}
	copy(buf[16:20], p.YIAddr)
	copy(buf[20:24], p.SIAddr)
	if p.GIAddr != nil {
		copy(buf[24:28], p.GIAddr.To4())
	}
	copy(buf[28:34], p.CHAddr)
	copy(buf[44:108], p.SName[:])
	copy(buf[108:236], p.File[:])

	// Magic cookie
	buf[236] = 99
	buf[237] = 130
	buf[238] = 83
	buf[239] = 99

	// Serialize options
	i := 240
	for opt, data := range p.Options {
		if i+2+len(data) >= len(buf) {
			break
		}
		buf[i] = opt
		buf[i+1] = byte(len(data))
		copy(buf[i+2:], data)
		i += 2 + len(data)
	}
	buf[i] = OptEnd

	return buf[:i+1]
}
