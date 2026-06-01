# YSwitch

[中文](README.md) | English

**YSwitch** is an open-source desktop tool for quickly switching between multiple Claude and Codex accounts. Built with [Wails](https://wails.io), Go backend + Vue frontend, currently Windows only.

## Screenshots

| Light Mode | Dark Mode |
|-----------|----------|
| ![Light Mode](assets/screenshots/light.png) | ![Dark Mode](assets/screenshots/dark.png) |

---

## Features

- **Account Saving**: Save the current logged-in account credentials to a local account library. Claude and Codex are managed independently, supporting multiple accounts each.
- **One-Click Switching**: Select the target account and click Switch — the tool automatically replaces the local credential files and restarts the corresponding client, no manual steps required.
- **Quota Viewing**: Refresh and display the plan type and remaining quota for each account.
- **Local Vault**: Saved account files stay on your machine. Quota refresh uses the corresponding official service API only.

---

## Login Method

**This tool does not implement any login logic. All logins are completed through the official clients.**

| Account Type | Login Method |
|-------------|-------------|
| **Claude** | Log in via the official Claude Code extension in VSCode |
| **Codex** | Log in via the official Codex desktop client |

After logging in, YSwitch reads, backs up, and replaces the corresponding credential files locally. When you refresh quota, it sends the access token only to the official ChatGPT or Anthropic quota endpoint; it does not upload credentials to any third-party server.

---

## Quick Start

### Requirements

- [Go](https://go.dev/) 1.21+
- [Node.js](https://nodejs.org/) 18+
- [Wails CLI](https://wails.io/docs/gettingstarted/installation) v2

### Development Mode

```bash
wails dev
```

### Build Release

```bash
wails build
```

---

## Usage

### Claude Accounts

1. Log in to account A via the Claude Code extension in VSCode, then open YSwitch.
2. Switch to the Claude tab and click **Save Current Account** to store account A in the library.
3. Manually delete or move `~/.claude/.credentials.json`, then close and reopen VSCode.
4. Log in to account B via the Claude Code extension, then return to YSwitch and click **Save Current Account** again.
5. To switch later: select the target account and click **Switch** — the tool will automatically replace the credential file and restart VSCode.

### Codex Accounts

1. Log in to account A via the official Codex desktop client, then open YSwitch.
2. Switch to the Codex tab and click **Save Current Account** to store account A in the library.
3. Exit the Codex client and log in to account B, then return to YSwitch and click **Save Current Account** again.
4. To switch later: select the target account and click **Switch** — the tool will automatically replace the credential file and restart Codex.

---

## ⭐ Support the Project

If YSwitch is helpful to you, consider giving it a Star ⭐!

---

## Disclaimer

- This project is **fully open-source**, intended for learning and personal use only. No commercial services are provided.
- The sole function of this tool is to manage and switch locally stored credential files for already-logged-in accounts. **It does not involve any cracking, bypassing, or policy-violating operations.**
- If your account is banned, restricted, or you suffer any other loss while using this tool, **this project bears no responsibility**. Please review the relevant platform terms of service before deciding whether to use it.
