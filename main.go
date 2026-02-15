package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/ars1364/go-pxe/dhcp"
	"github.com/ars1364/go-pxe/httpserver"
	"github.com/ars1364/go-pxe/tftp"
)

func main() {
	iface := flag.String("iface", "en7", "Network interface to listen on")
	serverIP := flag.String("ip", "10.0.0.1", "Server IP address on the PXE interface")
	dhcpStart := flag.String("dhcp-start", "10.0.0.100", "DHCP range start")
	dhcpEnd := flag.String("dhcp-end", "10.0.0.200", "DHCP range end")
	tftpRoot := flag.String("tftp-root", "./tftp", "TFTP root directory")
	httpRoot := flag.String("http-root", "./http", "HTTP root directory")
	httpPort := flag.Int("http-port", 8080, "HTTP server port")
	bootFile := flag.String("boot-file", "bootx64.efi", "PXE boot filename (UEFI)")
	flag.Parse()

	fmt.Println("=== Go PXE Boot Server ===")
	fmt.Printf("Interface:  %s\n", *iface)
	fmt.Printf("Server IP:  %s\n", *serverIP)
	fmt.Printf("DHCP Range: %s - %s\n", *dhcpStart, *dhcpEnd)
	fmt.Printf("TFTP Root:  %s\n", *tftpRoot)
	fmt.Printf("HTTP Root:  %s\n", *httpRoot)
	fmt.Printf("Boot File:  %s\n", *bootFile)
	fmt.Println()

	// Validate interface
	ifi, err := net.InterfaceByName(*iface)
	if err != nil {
		log.Fatalf("Interface %s not found: %v", *iface, err)
	}
	fmt.Printf("Interface %s MAC: %s\n", ifi.Name, ifi.HardwareAddr)

	// Create directories if needed
	os.MkdirAll(*tftpRoot, 0755)
	os.MkdirAll(*httpRoot, 0755)

	// Start DHCP server
	dhcpSrv := dhcp.NewServer(dhcp.Config{
		Interface:  *iface,
		ServerIP:   net.ParseIP(*serverIP),
		RangeStart: net.ParseIP(*dhcpStart),
		RangeEnd:   net.ParseIP(*dhcpEnd),
		SubnetMask: net.IPv4Mask(255, 255, 255, 0),
		BootFile:   *bootFile,
		TFTPServer: *serverIP,
	})
	go func() {
		if err := dhcpSrv.ListenAndServe(); err != nil {
			log.Fatalf("DHCP server error: %v", err)
		}
	}()

	// Start TFTP server
	tftpSrv := tftp.NewServer(*tftpRoot)
	go func() {
		if err := tftpSrv.ListenAndServe(":69"); err != nil {
			log.Fatalf("TFTP server error: %v", err)
		}
	}()

	// Start HTTP server
	go func() {
		addr := fmt.Sprintf(":%d", *httpPort)
		if err := httpserver.ListenAndServe(addr, *httpRoot); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	fmt.Println()
	fmt.Println("All services started. Waiting for PXE clients...")
	fmt.Println("Press Ctrl+C to stop.")

	// Wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("\nShutting down.")
}
