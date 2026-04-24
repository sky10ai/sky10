# sky10

Encrypted storage and agent coordination over P2P. Your data encrypted, your keys yours, no server sees plaintext.

Built in Go. Runs on macOS, Linux, and Windows. S3 optional.

## Install

macOS/Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/sky10ai/sky10/main/install.sh | bash
```

Windows:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/sky10ai/sky10/main/install.ps1 | iex"
```

Installs to `~/.bin/sky10` on macOS/Linux or `%LOCALAPPDATA%\sky10\bin\sky10.exe` on Windows. The installer also sets up the background daemon and the tray/menu app when the release includes a menu asset for the platform.

## License

Apache License 2.0
