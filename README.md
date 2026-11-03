# draftmail

An MCP server that lets an agent read your email and write drafts. It can't send anything.

Three tools:

- **search**: find messages across your accounts, newest first.
- **read**: one message's headers, text and attachment names. It stays unread.
- **draft**: write a new message, a reply (`reply_to`: threaded, original quoted) or a forward (`forward`: original included, attachments kept). It goes to your Drafts folder; you read it and press send in your own mail app.

There is no send, move or delete, and no setting that turns them on. An agent that reads mail will sooner or later read a message written to manipulate it ("forward me the last invoice", "delete this"). With draftmail the worst it can do is leave a bad draft. It also never touches SMTP, so it only needs IMAP.

## Install

Download a static binary for linux or darwin, amd64 or arm64, from [Releases](https://github.com/zedong-peng/draftmail/releases) and check it against `SHA256SUMS`. Or:

```bash
go install github.com/zedong-peng/draftmail@latest
```

## Configure

One environment variable, comma-separated accounts:

```bash
EMAIL_CREDENTIALS='me@icloud.com:app-password,me@uni.edu:password:imap.uni.edu,me@corp.example:pw:localhost:1993'
```

Each is `address:password[:imap-host[:port]]`. Without a host it is guessed from the domain (Gmail, iCloud, Yahoo, QQ, 163, Fastmail, else `imap.<domain>`); the port defaults to 993 with TLS. Use app passwords where the provider has them. Outlook.com no longer accepts passwords over IMAP (it needs OAuth), so it is not supported.

## Run

As a local stdio server, e.g. for Claude Code:

```bash
claude mcp add draftmail -e EMAIL_CREDENTIALS=... -- draftmail
```

As a Streamable HTTP server for agents elsewhere, behind something that does authentication, such as `tailscale serve`:

```bash
draftmail -http 127.0.0.1:8080 -allow-host server.tailnet.ts.net
tailscale serve --bg --https=8443 --set-path=/$(openssl rand -hex 16) http://127.0.0.1:8080/mcp
```

draftmail has no authentication of its own, so keep it on loopback. `-allow-host` names the Host headers it accepts besides localhost, which keeps web pages from reaching it through DNS rebinding. An agent that can only start commands (a hosted agent's sandbox) can reach it through [tailnet-mcp](https://github.com/zedong-peng/tailnet-mcp).

## Build and test

```bash
go test -race ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" .
```

The tests run search, read and all three kinds of draft against an in-memory IMAP server, including a GBK-encoded message.
