# Go PXE Boot Server

Self-contained PXE boot server for macOS: DHCP + TFTP + HTTP in a single binary. Tested and working with HPE Gen9 UEFI PXE boot.

## Quick Start

```bash
cd go-pxe
go build -o go-pxe .
sudo ./go-pxe \
  -iface en7 \
  -ip 10.0.0.1 \
  -dhcp-start 10.0.0.100 \
  -dhcp-end 10.0.0.200 \
  -tftp-root ../tftp \
  -http-root ../http \
  -http-port 8080 \
  -boot-file grubx64.efi
```

## Directory Structure

```
pxeboot/
├── dnsmasq.conf                              # Alternative dnsmasq config (if not using go-pxe)
├── go-pxe/                                   # This Go project (DHCP + TFTP + HTTP)
├── tftp/
│   ├── grubx64.efi                           # UEFI GRUB bootloader
│   ├── vmlinuz                               # Linux kernel (from AlmaLinux or Ubuntu ISO)
│   ├── initrd.img                            # AlmaLinux initramfs
│   ├── initrd                                # Ubuntu initramfs
│   └── grub/
│       └── grub.cfg                          # GRUB boot menu (multi-OS)
└── http/
    ├── almalinux97/                          # Extracted AlmaLinux 9.7 DVD (repo for Anaconda)
    ├── AlmaLinux-9.7-x86_64-dvd.iso          # AlmaLinux DVD ISO (12 GB)
    ├── ubuntu-24.04.4-live-server-amd64.iso  # Ubuntu live server ISO (3.2 GB)
    └── almalinux.img                         # AlmaLinux raw disk image (10 GB, for dd workflow)
```

## Two Installation Workflows

### Workflow 1: AlmaLinux — Anaconda Installer (Recommended)

PXE boots directly into the AlmaLinux Anaconda installer GUI. You walk through disk partitioning, user setup, and package selection interactively.

**Setup:**

```bash
# Download AlmaLinux DVD ISO
# Iranian mirrors: https://mirror.0-1.ir/almalinux/9/isos/x86_64/
# Official: https://repo.almalinux.org/almalinux/9/isos/x86_64/
curl -LO https://repo.almalinux.org/almalinux/9/isos/x86_64/AlmaLinux-9.7-x86_64-dvd.iso
mv AlmaLinux-9.7-x86_64-dvd.iso http/

# Extract PXE boot files (vmlinuz + initrd.img)
7z e http/AlmaLinux-9.7-x86_64-dvd.iso images/pxeboot/vmlinuz images/pxeboot/initrd.img -otftp/ -y

# Extract ISO contents for HTTP repo (Anaconda needs directory structure, not raw ISO)
mkdir -p http/almalinux97
cd http/almalinux97 && 7z x ../AlmaLinux-9.7-x86_64-dvd.iso -y
```

**GRUB config (`tftp/grub/grub.cfg`):**
```
menuentry "Install AlmaLinux 9.7 (Anaconda)" {
    linux vmlinuz ip=dhcp inst.repo=http://10.0.0.1:8080/almalinux97/
    initrd initrd.img
}
```

> **Important:** `inst.repo` must point to the extracted directory (containing `.treeinfo`), NOT to the raw ISO file. Anaconda cannot fetch a repo from an ISO URL over HTTP.

**Boot flow:**
1. PXE → GRUB loads AlmaLinux `vmlinuz` + `initrd.img` via TFTP
2. Kernel boots, Anaconda fetches repo from `http://10.0.0.1:8080/almalinux97/`
3. Anaconda GUI launches — configure disk, users, packages interactively
4. Installer writes AlmaLinux to disk and reboots

### Workflow 2: Ubuntu — Live Boot (for dd deploy or rescue)

PXE boots Ubuntu 24.04 live server. Use this either for a standard Ubuntu install, as a rescue shell, or to `dd` a raw disk image (like `almalinux.img`) to the target.

**Setup:**

```bash
# Download Ubuntu ISO
curl -LO https://releases.ubuntu.com/24.04.4/ubuntu-24.04.4-live-server-amd64.iso
mv ubuntu-24.04.4-live-server-amd64.iso http/

# Extract kernel/initrd from ISO
7z e http/ubuntu-24.04.4-live-server-amd64.iso casper/vmlinuz casper/initrd -otftp/ -y
# Rename: casper/vmlinuz -> vmlinuz, casper/initrd -> initrd (already in tftp/)
```

**GRUB config:**
```
menuentry "Ubuntu 24.04 Live (for dd deploy or rescue)" {
    linux vmlinuz ip=dhcp url=http://10.0.0.1:8080/ubuntu-24.04.4-live-server-amd64.iso
    initrd initrd
}
```

**dd workflow (to deploy pre-built AlmaLinux disk image):**
```bash
# From the live Ubuntu shell on the target server:
curl http://10.0.0.1:8080/almalinux.img | dd of=/dev/sda bs=4M status=progress
reboot
```

## Hardware Setup

1. Connect Mac to target server via Ethernet (USB-C/Thunderbolt adapter → direct cable)
2. Set static IP on the Ethernet interface:

```bash
# Find your interface (look for USB/Thunderbolt Ethernet)
networksetup -listallhardwareports

# Set static IP
sudo ifconfig en7 10.0.0.1 netmask 255.255.255.0 up
```

3. Boot target server, select PXE IPv4 (UEFI mode) from boot menu (F12/F11)

## Key Fixes (HPE Gen9 Compatibility)

### DHCP: Global broadcast for OFFER/ACK

HPE Gen9 UEFI PXE ROMs filter on IP destination and **reject subnet-directed broadcasts** (e.g., `10.0.0.255`). They only accept packets addressed to `255.255.255.255`.

**Fix:** Send DHCP replies to `255.255.255.255:68` (global broadcast) instead of subnet broadcast. Fall back to subnet broadcast only if global fails.

### TFTP: Option negotiation (RFC 2347)

HP UEFI PXE clients request `blksize` and `tsize` options in TFTP RRQ. Without an OACK response, the client either aborts or falls back to 512-byte blocks (causing 2.3MB `grubx64.efi` to transfer extremely slowly or time out).

**Fix:** Implement OACK responses supporting:
- `blksize` — negotiate up to 1468 bytes (Ethernet MTU - headers)
- `tsize` — report file size before transfer

This reduced `grubx64.efi` transfer from timing out to completing in <1 second.

## Alternative: dnsmasq

If you prefer dnsmasq over the Go binary:

```bash
# Start dnsmasq (DHCP + TFTP)
sudo dnsmasq --no-daemon --conf-file=../dnsmasq.conf

# Start HTTP server separately
cd ../http && python3 -m http.server 8080
```

> **Note:** On macOS, dnsmasq with `bind-interfaces` can get stuck at 100% CPU on some setups. The go-pxe binary avoids this issue entirely.

## Internet Sharing (Post-Install)

To give the PXE-booted server internet access through your Mac:

```bash
# Enable IP forwarding
sudo sysctl -w net.inet.ip.forwarding=1

# NAT via your Mac's internet interface (e.g., en0 = Wi-Fi)
echo "nat on en0 from 10.0.0.0/24 to any -> (en0)" | sudo pfctl -ef -
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| DISCOVER loops (no REQUEST) | Ensure replies go to `255.255.255.255`, not subnet broadcast |
| TFTP transfer never completes | Server must support `blksize`/`tsize` OACK negotiation |
| Anaconda "failed to fetch" | `inst.repo` must point to extracted ISO directory, not raw ISO file |
| GRUB shows "file not found" for `.lst` files | Non-fatal — GRUB modules `command.lst`, `fs.lst`, etc. are optional |
| Server doesn't PXE boot | Check BIOS: must be UEFI mode, network boot enabled |
| dnsmasq 100% CPU on macOS | Use go-pxe binary instead |
| TFTP timeout on large files | Ensure `blksize` negotiation is working (check for OACK in logs) |

## Lessons Learned

1. **PXE ROMs filter on IP destination**: HP UEFI only accepts `255.255.255.255` broadcast, not subnet-directed `10.0.0.255` — even though both arrive as ethernet broadcast frames.
2. **TFTP option negotiation is mandatory for UEFI**: Modern PXE clients request `blksize`/`tsize`. Without OACK support, large file transfers fail.
3. **Anaconda needs a repo directory, not an ISO URL**: `inst.repo=http://server/file.iso` does not work. Extract the ISO and serve the directory structure (must contain `.treeinfo`).
4. **macOS broadcast routing**: Use `IP_BOUND_IF` to pin UDP sockets to the correct interface.
5. **BOOTP minimum packet size**: Some PXE ROMs reject DHCP packets smaller than 548 bytes.
6. **dnsmasq on macOS is unreliable**: With `bind-interfaces` it can spin at 100% CPU. The Go binary is more predictable.
