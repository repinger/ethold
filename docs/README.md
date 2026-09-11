# ETHOL Auto-Presence Documentation

This document gives an overview of the ETHOL Auto-Presence project.
The software runs as a background service.
It records student attendance automatically on the ETHOL PENS platform.

## Purpose

The software does these tasks:
- It connects to the Central Authentication Service (CAS) of PENS.
- It finds the active courses for the current semester.
- It monitors course attendance sessions.
- It sends attendance codes when sessions become active.
- It sends status updates to a Telegram chat.

## Document List

Read the documents in this table for more information:

| Document | Description |
|---|---|
| [Architecture](architecture.md) | Shows the system design, components, and data flow. |
| [Configuration](configuration.md) | Explains environment variables and configuration files. |
| [Authentication](authentication.md) | Describes the CAS single sign-on process and session management. |
| [Components](components.md) | Describes each code module in the `internal/ethol` package. |
| [API Reference](api-reference.md) | Lists all ETHOL HTTP endpoints that the client calls. |
| [Telegram Commands](telegram-commands.md) | Lists all bot commands and their output. |
| [Deployment](deployment.md) | Gives instructions for Docker and standalone binary setup. |
| [Development](development.md) | Explains how to build, test, and change the code. |

## Quick Start

Follow these steps to run the software:

1. Copy the example environment file:
   ```bash
   cp .env.example .env
   ```
2. Open the `.env` file in an editor. Enter your PENS username and password.
3. Build the program:
   ```bash
   go build -o ethol-autopresence ./cmd/ethol-autopresence
   ```
4. Run the program for one check:
   ```bash
   ./ethol-autopresence --once
   ```
5. Run the program as a service:
   ```bash
   ./ethol-autopresence
   ```
