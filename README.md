# AGNOS Waiting for Internet Debug

This is a read-only diagnostic tool for AGNOS/openpilot setup screens that are stuck at **Waiting for internet**.

It scans your local network, finds comma/openpilot devices that accept SSH as `comma`, and runs the same internet checks that openpilot setup uses against:

```text
https://openpilot.comma.ai
```

The tool does **not** install software, modify files, restart services, or reboot the device.

## Windows Quick Start

1. Connect your Windows computer to the same Wi-Fi/network as the comma device.
2. On the device, open **Advanced internet settings** if you can and note the **IP address**.
3. Download the latest Windows executable:

   [agnos-waiting-for-internet-debug.exe](https://github.com/ophwug/agnos-waiting-for-internet-debug/releases/latest/download/agnos-waiting-for-internet-debug.exe)

4. Double-click the executable.
5. Choose whether to enter the device IP address or scan the local network.
6. Share the printed output and a screenshot of the device setup screen.

If double-clicking closes too quickly, open Command Prompt in the download folder and run:

```cmd
agnos-waiting-for-internet-debug.exe
```

## What It Checks

The current AGNOS setup flow checks internet connectivity by making a fast `HEAD` request to `https://openpilot.comma.ai`. Older setup code used a `GET` request. This tool runs both over SSH from the device itself, then reports whether setup is likely seeing:

- `Continue`
- `Continue without Wi-Fi`
- `Waiting for internet`

It also prints read-only network context from the device, including default route, DNS config, device time, model, and OS version when available.

## Options

```text
--ip <addr>          Debug one known device IP instead of scanning.
--cidr <cidr>        Scan a specific IPv4 subnet, such as 192.168.1.0/24.
--parallel <n>       Maximum concurrent SSH probes. Default: 64.
--timeout <duration> SSH probe timeout. Default: 750ms.
--json               Print machine-readable JSON.
```

Examples:

```cmd
agnos-waiting-for-internet-debug.exe --ip 192.168.1.42
agnos-waiting-for-internet-debug.exe --cidr 192.168.1.0/24
agnos-waiting-for-internet-debug.exe --json
```

## macOS and Linux

The tool also builds for macOS and Linux:

```bash
curl -L https://github.com/ophwug/agnos-waiting-for-internet-debug/releases/latest/download/agnos-waiting-for-internet-debug-darwin -o agnos-waiting-for-internet-debug
chmod +x agnos-waiting-for-internet-debug
./agnos-waiting-for-internet-debug
```

```bash
curl -L https://github.com/ophwug/agnos-waiting-for-internet-debug/releases/latest/download/agnos-waiting-for-internet-debug-linux -o agnos-waiting-for-internet-debug
chmod +x agnos-waiting-for-internet-debug
./agnos-waiting-for-internet-debug
```

Automatic subnet detection is implemented for Windows, macOS, and Linux. If it cannot detect your active LAN, use `--ip` or `--cidr`.

## For Developers

Requirements:

- Go 1.22 or newer
- GitHub CLI if publishing releases manually

Commands:

```bash
go test ./...
make
make run
```

GitHub Actions builds the Windows, macOS, and Linux executables and publishes them to the `latest` release on every push to `main`.

## Credits

This was retooled from `ophwug/c2-neos-alt-fix-install`, which was itself a Go evolution of the original NEOS manual installer by `jyoung8607`.
