# EasyDrop

**English** · [Russian](README_RU.md)

**EasyDrop** is a lightweight CLI tool and Go-based MCP server designed to automate Docker application deployment to any Linux environment (localhost, VPS, bare-metal, or Raspberry Pi). 

It delivers a PaaS-like experience (similar to Railway or Fly.io) on your own infrastructure by combining an orchestrator, a reverse proxy (Nginx), and automated SSL certificate management (Certbot) into a single binary.

**Project Status: Under Active Development**
> **Note:** EasyDrop is currently an experimental project in its early development phase. Features described below are being actively implemented, and breaking changes may occur frequently. Not production-ready yet!

---

## Key Features

*   **One Core, Two Interfaces:** A unified deployment core accessible via either a developer-friendly terminal CLI or an MCP server for AI agents.
*   **Zero-Config Reverse Proxy:** Automatically generates Nginx configurations and provisions Let's Encrypt SSL certificates right out of the box.
*   **Remote & Local Builds:** Supports building Docker images directly on the target host (no external registry required) or building locally and pushing to a private registry.
*   **Zero-to-Hero Bootstrapping:** Automatically inspects and prepares clean Linux servers by installing Docker Engine, Docker Compose, and configuring firewall rules.
*   **AI-Native (MCP):** Native Model Context Protocol integration allows Cursor, Claude Code, and other AI assistants to manage infrastructure via natural language.

---

## Quick Start

### 1. Initialize a Project
Run the following command in your project root directory:
```bash
easydrop init
```
The tool will automatically analyze your directory assets and generate a tailored `easydrop.toml` configuration file.

### 2. Configure Your Environment
Edit the generated `easydrop.toml` to map your target server details and domain:

```toml
[app]
name = "my-awesome-api"
port = 8080

[server]
host = "185.178.21.42" # Use "localhost" or "127.0.0.1" for local deployment
user = "root"
ssh_key = "~/.ssh/id_rsa"

[build]
strategy = "remote"

[driver]
type = "single"

[nginx]
domain = "my-project.com"
ssl = true
```

### 3. Deploy
Deploy your stack to production with a single command:
```bash
easydrop deploy
```
EasyDrop will handle the SSH handshake, bootstrap Docker if missing, securely transfer code, build the image, provision Nginx/SSL, and orchestrate a zero-downtime (Blue-Green) container swap.

---

## Using with AI Assistants (MCP)

EasyDrop exposes a native **Model Context Protocol (MCP)** interface. You can link it to an AI client (such as Cursor or Claude Desktop) by adding it as an active MCP server:

*   **Type:** `stdio`
*   **Command:** `easydrop mcp-server` (or the absolute path to your compiled binary + `mcp-server` arg)

Once connected, you can prompt your AI assistant directly in chat:
> *"Deploy the current project to my server"*  
> *"Stream the logs and let me know why the database container is failing"*

---

## Project Documentation Structure

The core codebase documentation is decoupled by operational boundaries:
*   [PRD.md](PRD.md) – Product scope, operational logic, and functional requirements.
*   [ARCHITECTURE.md](ARCHITECTURE.md) – Component layouts, state management specifications, and internal Go package map.
*   [docs/implementation.md](docs/implementation.md) – Technical roadmap and exact build execution steps for the AI agent.
*   [docs/cli-spec.md](docs/cli-spec.md) – Full interface specification for terminal commands and execution flags.
*   [docs/mcp-spec.md](docs/mcp-spec.md) – JSON-RPC tool schemas and target context resources for LLMs.

---

## License

Distributed under the MIT License. See [LICENSE](LICENSE.md) for more information.
