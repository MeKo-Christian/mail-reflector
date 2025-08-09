# 📬 mail-reflector

A CLI tool written in Go that monitors an IMAP inbox for incoming messages from a specific sender and **reflects** them to a configured list of BCC recipients, preserving the original body and attachments.

Useful for automated redistribution of announcements (e.g. from a board member to a delegate list), with safety, structure, and optional logging.

---

## 🚀 Features

- Checks an IMAP inbox for unread messages from a configured address
- Forwards matching messages via SMTP
- Preserves:
  - Subject
  - Sender (`To` and `Reply-To`)
  - Plain text and HTML bodies
  - Attachments
- Sends to a list of BCC recipients

---

## 📦 Installation

```bash
git clone https://github.com/meko-christian/mail-reflector.git
cd mail-reflector
go build -o mail-reflector .
```

---

## ⚙️ Configuration

Create a `config.yaml` in the working directory:

```yaml
imap:
  server: imap.mailserver.com
  port: 993
  security: ssl
  username: YOUR_IMAP_USERNAME
  password: YOUR_IMAP_PASSWORD

filter:
  from:
    - you@your-provider.com
    - another@your-provider.com

recipients:
  - person1@example.com
  - person2@example.com

smtp:
  server: smtp.mailserver.com
  port: 465
  security: ssl
  username: YOUR_SMTP_USERNAME
  password: YOUR_SMTP_PASSWORD
```

---

## 🔧 Usage

Check and forward messages:

```bash
./mail-reflector check
```

With verbose output:

```bash
./mail-reflector check --verbose
```

Show version:

```bash
./mail-reflector version
```

---

## Usage with Cron

Example cron entry to check every 10 minutes:

```plain
*/10 * * * * /path/to/mail-reflector check >> /var/log/mail-reflector.log 2>&1
```

Systemd users may prefer a timer + service unit with StandardOutput=journal.

---

## Running as a service

You can run this tool as a systemd service for continuous background operation. Here’s an example systemd service file:

```plain
[Unit]
Description=Mail Reflector Service
After=network.target

[Service]
ExecStart=/path/to/mail-reflector
WorkingDirectory=/path/to
Restart=always
User=<your-user>
Group=<your-group>

[Install]
WantedBy=multi-user.target
```

Place this file at `/etc/systemd/system/mail-reflector.service` and enable it with:

```bash
sudo systemctl enable mail-reflector
sudo systemctl start mail-reflector
```

## 📄 License

MIT License.
