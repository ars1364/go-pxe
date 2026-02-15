# go-pxe

A self-contained PXE boot server written in Go. Zero external dependencies — DHCP, TFTP, and HTTP servers are all built-in.

Designed for bare-metal server provisioning over a direct Ethernet connection (e.g., laptop to server).

## Features

- **DHCP Server** — Assigns IPs and advertises PXE boot parameters (UEFI and Legacy BIOS)
- **TFTP Server** — Serves bootloader (GRUB EFI), kernel, and initrd
- **HTTP Server** — Serves OS installation ISO for the installer to fetch
- **Zero Dependencies** — Single binary, no dnsmasq/tftpd/nginx required
- **Cross-Platform** — Runs on macOS, Linux, Windows (anywhere Go compiles)

## Quick Start

### 1. Build

```bash
go build -o go-pxe .
```

### 2. Prepare Boot Files

Create the directory structure:

```
./tftp/
  grubx64.efi          # UEFI bootloader (from Ubuntu grub-efi-amd64-signed package)
  vmlinuz              # Linux kernel (from ISO casper/vmlinuz)
  initrd               # Initial ramdisk (from ISO casper/initrd)
  grub/
    grub.cfg           # GRUB configuration

./http/
  ubuntu-24.04.4-live-server-amd64.iso   # OS installation ISO
```

#### Extract boot files from Ubuntu ISO:

```bash
# Install p7zip if needed
brew install p7zip   # macOS
apt install p7zip    # Linux

# Extract kernel and initrd
cd tftp
7z e /path/to/ubuntu-24.04.4-live-server-amd64.iso casper/vmlinuz casper/initrd -y

# Get GRUB EFI bootloader
# From Ubuntu's grub-efi-amd64-signed package:
curl -sLO "http://archive.ubuntu.com/ubuntu/pool/main/g/grub2-signed/grub-efi-amd64-signed_1.214+2.14~git20250718.0e36779-1ubuntu4_amd64.deb"
ar x grub-efi-amd64-signed_*.deb
zstd -d data.tar.zst && tar xf data.tar
cp usr/lib/grub/x86_64-efi-signed/grubnetx64.efi.signed grubx64.efi
rm -rf usr debian-binary control.tar.* data.tar* grub-efi-amd64-signed_*.deb
```

#### Create GRUB config:

```bash
mkdir -p tftp/grub
cat > tftp/grub/grub.cfg << 'EOF'
set timeout=30
set default=0

menuentry "Install Ubuntu 24.04.4 Server" {
    linux vmlinuz ip=dhcp url=http://10.0.0.1:8080/ubuntu-24.04.4-live-server-amd64.iso autoinstall
    initrd initrd
}

menuentry "Install Ubuntu 24.04.4 Server (safe graphics)" {
    linux vmlinuz ip=dhcp url=http://10.0.0.1:8080/ubuntu-24.04.4-live-server-amd64.iso autoinstall nomodeset
    initrd initrd
}

menuentry "Boot from local disk" {
    exit
}
EOF
```

### 3. Connect & Configure Network

Connect your machine to the target server via Ethernet (direct cable or switch).

Assign a static IP to the interface:

```bash
# macOS (replace en7 with your interface)
networksetup -setmanual "AX88179B" 10.0.0.1 255.255.255.0

# Linux
sudo ip addr add 10.0.0.1/24 dev eth0
sudo ip link set eth0 up
```

### 4. Run

```bash
sudo ./go-pxe \
  --iface en7 \
  --ip 10.0.0.1 \
  --tftp-root ./tftp \
  --http-root ./http
```

> **Note:** Requires root/sudo for DHCP (port 67) and TFTP (port 69).

### 5. Boot the Server

1. Power on the target server
2. Press **F12** (or enter BIOS) for one-time network boot
3. Ensure **UEFI mode** is enabled
4. Select the network adapter connected to your machine
5. GRUB menu appears → select "Install Ubuntu"
6. The installer downloads the ISO via HTTP and proceeds normally

## Command-Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `--iface` | `en7` | Network interface to listen on |
| `--ip` | `10.0.0.1` | Server IP address |
| `--dhcp-start` | `10.0.0.100` | DHCP range start |
| `--dhcp-end` | `10.0.0.200` | DHCP range end |
| `--tftp-root` | `./tftp` | TFTP root directory |
| `--http-root` | `./http` | HTTP root directory |
| `--http-port` | `8080` | HTTP server port |
| `--boot-file` | `bootx64.efi` | PXE boot filename (UEFI) |

## Architecture

```
┌─────────────┐                    ┌──────────────┐
│  PXE Client │──── DHCP ────────▶│  go-pxe      │
│  (Server)   │◀─── IP + Boot ───│              │
│             │                    │  DHCP :67    │
│             │──── TFTP ────────▶│  TFTP :69    │
│             │◀─── grubx64.efi ──│  HTTP :8080  │
│             │◀─── vmlinuz ──────│              │
│             │◀─── initrd ───────│              │
│             │                    │              │
│             │──── HTTP ────────▶│              │
│             │◀─── ISO (3.2GB) ──│              │
└─────────────┘                    └──────────────┘
```

## PXE Boot Flow

1. **DHCP DISCOVER** — Client broadcasts looking for a DHCP server
2. **DHCP OFFER** — Server offers IP + boot parameters (TFTP server, boot file)
3. **DHCP REQUEST/ACK** — Client accepts the offer
4. **TFTP** — Client downloads `grubx64.efi` (UEFI bootloader)
5. **TFTP** — GRUB downloads `grub.cfg`, `vmlinuz`, `initrd`
6. **HTTP** — Kernel fetches the full ISO for installation

## Tested With

- **Server:** HPE ProLiant DL360 Gen9 (UEFI, HP 331i 1Gb NIC)
- **Client:** macOS (Apple Silicon M3 Pro) with USB Ethernet adapter (AX88179B)
- **OS:** Ubuntu 24.04.4 LTS Server

## Troubleshooting

### Client keeps sending DHCP DISCOVERs
- Check that the Ethernet link is up (`status: active` in `ifconfig`)
- Ensure no other DHCP server is on the network
- On macOS, the server uses subnet broadcast (10.0.0.255) for reliability

### GRUB shows "file not found"
- Verify `grubx64.efi` is in the TFTP root
- Check that `grub/grub.cfg` exists relative to TFTP root
- GRUB module files (command.lst, fs.lst) missing is non-fatal

### ISO download fails during install
- Ensure the HTTP server is running and the ISO path matches `grub.cfg`
- Check that the ISO filename in `grub.cfg` matches the actual file

## License

MIT
