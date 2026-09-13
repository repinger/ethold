# Configuration

This document explains the configuration options for the application.

Configuration values are resolved with the following precedence:

1. **Command line flags** (highest)
2. **System environment variables** (`os.Getenv`)
3. **Configuration file** (`.env`) (lowest)

If the `.env` file does not exist, the application will run directly using system environment variables or CLI flags without error.

## Configuration File

The application can read configuration from an environment file.
The default file path is `.env` in the current working directory.
You can specify a different path with the `--config` flag.

The file parser supports these features:

- Standard `KEY=VALUE` pairs.
- Lines with an `export ` prefix.
- Single quotes and double quotes around values.
- Comments that start with a `#` character.
- Blank lines and whitespace trimming.

## Environment Variables

The table below lists all supported environment variables:

| Variable              | Required | Default | Description                                                                                     |
| --------------------- | -------- | ------- | ----------------------------------------------------------------------------------------------- |
| `ETHOL_EMAIL`         | Yes      | None    | The PENS CAS username or NRP.                                                                   |
| `ETHOL_PASSWORD`      | Yes      | None    | The PENS CAS account password.                                                                  |
| `TELEGRAM_TOKEN`      | No       | None    | The API token from BotFather for Telegram updates.                                              |
| `TELEGRAM_CHAT_ID`    | No       | None    | The Telegram chat ID that receives messages and issues commands.                                |
| `ETHOL_AUTO_PRESENCE` | No       | `false` | Enables automated attendance scanning and submission (`true`, `1`, `yes`). Disabled by default. |

> **Note:** Both `ETHOL_EMAIL` and `ETHOL_PASSWORD` are required (via flag, system env, or file).
> If Telegram variables are empty, the application disables the Telegram integration.
> If `ETHOL_AUTO_PRESENCE` is disabled or unset, the daemon runs in academic-only mode without scanning or submitting attendance.
> `ETHOL_AUTO_PRESENCE` has **no corresponding CLI flag**; it can only be set via the `.env` file or a system environment variable.

## Example File

Here is an example `.env` file:

```bash
# ETHOL PENS Credentials
ETHOL_EMAIL=1234567890
ETHOL_PASSWORD=secret_password_here

# Telegram Notification (Optional)
TELEGRAM_TOKEN=123456789:ABCdefGhIJKlmNoPQRsTUVwxyZ
TELEGRAM_CHAT_ID=987654321

# Auto-Presence (Optional, disabled by default)
ETHOL_AUTO_PRESENCE=true
```

## Command Line Flags

You can control the application using command line flags:

| Flag                 | Default              | Description                                                           |
| -------------------- | -------------------- | --------------------------------------------------------------------- |
| `--config`           | `.env`               | Path to the configuration file (optional if env vars/flags provided). |
| `--username`         | None                 | ETHOL CAS username or NRP.                                            |
| `--password`         | None                 | ETHOL CAS password.                                                   |
| `--telegram-token`   | None                 | Telegram bot token.                                                   |
| `--telegram-chat-id` | None                 | Telegram chat ID.                                                     |
| `--state`            | `attended_keys.json` | Path to the JSON state file.                                          |
| `--once`             | `false`              | Run a single scan pass and exit immediately.                          |
| `--concurrency`      | `4`                  | Number of worker goroutines for course checks.                        |
| `--verbose`          | `false`              | Show debug log messages in the console.                               |

### Example Flag Commands

Run directly with CLI credentials:

```bash
./ethold --username 1234567890 --password secret_password_here --once
```

Run one scan with verbose logging:

```bash
./ethold --config /etc/ethol/.env --once --verbose
```

Run with 8 worker threads and a custom state file:

```bash
./ethold --concurrency 8 --state /var/lib/ethol/state.json
```
