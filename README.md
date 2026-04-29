# Go PXE Boot Server

Self-contained PXE boot server for macOS: DHCP + TFTP + HTTP in a single binary.

> **Note:** The Go DHCP implementation has a known issue with HPE Gen9 (and possibly other) UEFI PXE ROMs that reject its OFFER packets. Use **dnsmasq** for production PXE boots (see below). The Go binary is useful for environments where dnsmasq is not available.

## What Actually Works: dnsmasq + HTTP

This is the tested, working method for PXE booting HPE Gen9 servers from macOS.

### Prerequisites

```bash
# Install dnsmasq (if not already installed)
brew install dnsmasq

# Python 3 (for HTTP server, comes with macOS)
python3 --version
```

### Directory Structure

```
pxeboot/
├── dnsmasq.conf                              # Production DHCP/TFTP config
├── tftp/
│   ├── grubx64.efi                           # UEFI GRUB bootloader
│   ├── vmlinuz                               # Linux kernel (extracted from Ubuntu ISO)
│   ├── initrd                                # initramfs (extracted from Ubuntu ISO)
│   └── grub/
│       └── grub.cfg                          # GRUB boot menu
└── http/
    ├── ubuntu-24.04.4-live-server-amd64.iso  # Ubuntu installer ISO (3.2 GB)
    ├── almalinux.img                         # AlmaLinux raw disk image (10 GB, converted from QCOW2)
    ├── AlmaLinux-9-latest-x86_64-boot.iso    # AlmaLinux network install ISO (not wired into GRUB)
    └── AlmaLinux-9-latest-x86_64-minimal.iso # AlmaLinux minimal ISO (not wired into GRUB)
```

### Step-by-Step

#### 1. Connect Hardware

- Connect Mac to target server via Ethernet (direct cable or same switch)
- Identify your Ethernet interface:

```bash
networksetup -listallhardwareports
# Look for "Thunderbolt Ethernet" or "USB 10/100/1000 LAN" — note the Device (e.g., en7)
```

#### 2. Set Static IP on Ethernet Interface

```bash
sudo ifconfig en7 10.0.0.1 netmask 255.255.255.0 up
# Verify:
ifconfig en7 | grep inet
# Should show: inet 10.0.0.1 netmask 0xffffff00 broadcast 10.0.0.255
```

#### 3. Prepare Boot Assets

**For Ubuntu 24.04:**

```bash
cd /path/to/pxeboot

# Download ISO (if not already present)
curl -LO https://releases.ubuntu.com/24.04.4/ubuntu-24.04.4-live-server-amd64.iso
mv ubuntu-24.04.4-live-server-amd64.iso http/

# Extract kernel and initrd from ISO
mkdir -p /tmp/ubuntu-mount
hdiutil attach http/ubuntu-24.04.4-live-server-amd64.iso -mountpoint /tmp/ubuntu-mount
cp /tmp/ubuntu-mount/casper/vmlinuz tftp/vmlinuz
cp /tmp/ubuntu-mount/casper/initrd tftp/initrd
hdiutil detach /tmp/ubuntu-mount
```

**For AlmaLinux/RHEL (boot ISO):**

```bash
curl -LO https://repo.almalinux.org/almalinux/9/isos/x86_64/AlmaLinux-9-latest-x86_64-boot.iso
mv AlmaLinux-9-latest-x86_64-boot.iso http/

mkdir -p /tmp/alma-mount
hdiutil attach http/AlmaLinux-9-latest-x86_64-boot.iso -mountpoint /tmp/alma-mount
cp /tmp/alma-mount/images/pxeboot/vmlinuz tftp/vmlinuz
cp /tmp/alma-mount/images/pxeboot/initrd.img tftp/initrd
hdiutil detach /tmp/alma-mount
```

**Get GRUB EFI bootloader** (if not already present):

```bash
# From an existing Ubuntu system, or extract from the ISO:
# The grubx64.efi in this repo should work for most UEFI systems.
ls tftp/grubx64.efi
```

#### 4. Configure GRUB Menu

Edit `tftp/grub/grub.cfg`:

**For Ubuntu live/rescue shell:**
```
set timeout=30
set default=0

menuentry "Ubuntu 24.04 Live" {
    linux vmlinuz ip=dhcp url=http://10.0.0.1:8080/ubuntu-24.04.4-live-server-amd64.iso
    initrd initrd
}

menuentry "Boot from local disk" {
    exit
}
```

**For AlmaLinux network install:**
```
set timeout=30
set default=0

menuentry "Install AlmaLinux 9" {
    linux vmlinuz ip=dhcp inst.repo=https://repo.almalinux.org/almalinux/9/BaseOS/x86_64/os/
    initrd initrd
}

menuentry "Boot from local disk" {
    exit
}
```

#### 5. Start HTTP Server

```bash
cd /path/to/pxeboot/http
python3 -m http.server 8080 &
```

#### 6. Start dnsmasq (PXE DHCP + TFTP)

```bash
sudo dnsmasq \
  --no-daemon \
  --interface=en7 \
  --bind-interfaces \
  --dhcp-range=10.0.0.100,10.0.0.200,255.255.255.0,1h \
  --dhcp-option=option:router,10.0.0.1 \
  --dhcp-boot=grubx64.efi \
  --dhcp-match=set:efi-x86_64,option:client-arch,7 \
  --dhcp-match=set:efi-x86_64,option:client-arch,9 \
  --dhcp-boot=tag:efi-x86_64,grubx64.efi \
  --enable-tftp \
  --tftp-root=/path/to/pxeboot/tftp \
  --log-dhcp \
  --log-queries
```

> Replace `en7` with your Ethernet interface and paths accordingly.

#### 7. Boot the Target Server

1. Power on the server
2. Press **F12** (or F11) for the boot menu
3. Select **Network Boot** / **PXE** (UEFI mode)
4. The server will:
   - DHCP → get IP from dnsmasq
   - TFTP → download `grubx64.efi`
   - GRUB → load `grub.cfg`, show boot menu
   - Download `vmlinuz` + `initrd` via TFTP
   - Boot the kernel, which downloads the ISO via HTTP

### Expected dnsmasq Log Output

```
dnsmasq-dhcp: DHCPDISCOVER(en7) 20:67:7c:e5:54:ec
dnsmasq-dhcp: DHCPOFFER(en7) 10.0.0.198 20:67:7c:e5:54:ec
dnsmasq-dhcp: DHCPREQUEST(en7) 10.0.0.198 20:67:7c:e5:54:ec
dnsmasq-dhcp: DHCPACK(en7) 10.0.0.198 20:67:7c:e5:54:ec
dnsmasq-tftp: sent grubx64.efi to 10.0.0.198
dnsmasq-tftp: sent grub/grub.cfg to 10.0.0.198
dnsmasq-tftp: sent vmlinuz to 10.0.0.198
dnsmasq-tftp: sent initrd to 10.0.0.198
```

## Two Installation Workflows

This project supports two distinct workflows:

### Workflow 1: Ubuntu — PXE Boot the Installer (Recommended)

The Ubuntu ISO (`ubuntu-24.04.4-live-server-amd64.iso`) is a **live server installer**. PXE booting it launches the standard Ubuntu installer (Subiquity), which properly partitions the disk, installs packages, and configures the bootloader on the target server. This is the normal, clean way to install an OS via PXE.

The boot flow is:
1. PXE boot → GRUB loads `vmlinuz` + `initrd` via TFTP
2. Kernel boots, downloads the full ISO via HTTP
3. Ubuntu installer launches — walk through it interactively (or provide an `autoinstall` config)
4. Installer writes a proper installation to disk and reboots

No `dd`, no raw images — just a standard network install.

### Workflow 2: AlmaLinux — Clone a Pre-Built Disk Image

AlmaLinux is handled differently because we have a **pre-built VM disk image** rather than a bootable installer ISO. The files are:

| File | Format | Size | Description |
|------|--------|------|-------------|
| `AlmaLinux` (QCOW2) | QEMU QCOW2 v3 | 562 MB | Original VM disk image (sparse, compressed) |
| `almalinux.img` (raw) | Raw MBR disk image | 10 GB | Converted from QCOW2, ready to write to physical disk |

The QCOW2 is converted to raw format using:

```bash
qemu-img convert -f qcow2 -O raw AlmaLinux almalinux.img
```

> **Why raw?** QCOW2 is a QEMU-specific format — you can't write it directly to a physical disk. The raw image is a byte-for-byte copy of the virtual disk that can be written with `dd`.

To deploy it to a physical server:

1. PXE boot the target into **Ubuntu 24.04 Live** (using Workflow 1's GRUB menu, but drop to a shell instead of running the installer)
2. From the live Ubuntu shell on the target server:

```bash
# Find the target disk
lsblk

# Stream the raw image from Mac and write to disk
curl http://10.0.0.1:8080/almalinux.img | dd of=/dev/sda bs=4M status=progress

# Reboot into AlmaLinux
reboot
```

> **Note:** The AlmaLinux boot/minimal ISOs (`AlmaLinux-9-latest-x86_64-boot.iso`, `AlmaLinux-9-latest-x86_64-minimal.iso`) in `http/` are small network install ISOs. They are not currently wired into the GRUB menu. To PXE boot AlmaLinux's own installer, you would need to extract its `vmlinuz`/`initrd.img`, add a GRUB menu entry, and point `inst.repo` to an AlmaLinux mirror (see the example `grub.cfg` in section 4 above).

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
| DISCOVER loops (no REQUEST) | Use dnsmasq instead of go-pxe. HPE UEFI PXE ROMs are strict about DHCP packet format. |
| TFTP "User aborted" then succeeds | Normal — PXE ROM retries on first TFTP error. |
| GRUB shows "file not found" for `.lst` files | Non-fatal. GRUB modules `command.lst`, `fs.lst`, etc. are optional. |
| Server doesn't PXE boot | Check BIOS: must be UEFI mode, network boot enabled. |
| "No bootable device" | Verify Ethernet link is up: `ifconfig en7` should show `RUNNING`. |
| TFTP timeout | Check macOS firewall is disabled: `sudo /usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate` |

## Go PXE Binary (Experimental)

The Go binary (`go-pxe`) combines DHCP + TFTP + HTTP but has a known DHCP compatibility issue with strict PXE ROMs. It works with some clients but not HPE Gen9 UEFI.

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

## Lessons Learned

1. **HPE Gen9 UEFI PXE is extremely strict** about DHCP packet format. dnsmasq handles all the edge cases.
2. **macOS broadcast routing**: Outbound UDP broadcasts go through the default-route interface (Wi-Fi), not necessarily the Ethernet. Use `IP_BOUND_IF` or dnsmasq's `--bind-interfaces` to pin to the correct interface.
3. **BOOTP minimum packet size**: Some PXE ROMs reject DHCP packets smaller than 548 bytes.
4. **DHCP source port**: PXE ROMs reject replies not from port 67.
5. **Pre-built disk images (QCOW2/raw) are not PXE-bootable**: A VM disk image cannot be PXE booted directly. Convert QCOW2 to raw (`qemu-img convert -f qcow2 -O raw`), PXE boot a live Ubuntu, then `curl | dd` the raw image to the target disk. This is a workaround for deploying a pre-built OS — proper PXE installation (like the Ubuntu workflow) is preferred when an installer ISO is available.
