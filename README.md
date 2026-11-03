# imap-draft-mcp

IMAP for AI agents: search, read and write drafts. It can't send.

Three tools:

- **search**: find messages across your accounts, newest first.
- **read**: one message's headers, text and attachment names. It stays unread.
- **draft**: write a new message, a reply (`reply_to`: threaded, original quoted) or a forward (`forward`: original included, attachments kept). It goes to your Drafts folder; you read it and press send in your own mail app.

There is no send, move or delete, and no setting that turns them on. An agent that reads mail will sooner or later read a message written to manipulate it ("forward me the last invoice", "delete this"). Here the worst it can do is leave a bad draft. It never touches SMTP, so it only needs IMAP.

The whole server is one static binary, one environment variable and about 800 lines of Go on the [official MCP SDK](https://github.com/modelcontextprotocol/go-sdk).

## Install

Download a binary for linux or darwin, amd64 or arm64, from [Releases](https://github.com/zedong-peng/imap-draft-mcp/releases) and check it against `SHA256SUMS`. Or:

```bash
go install github.com/zedong-peng/imap-draft-mcp@latest
```

Or run the image `ghcr.io/zedong-peng/imap-draft-mcp`. It is also listed in the [MCP Registry](https://registry.modelcontextprotocol.io) as `io.github.zedong-peng/imap-draft-mcp`.

## Configure

One environment variable, comma-separated accounts:

```bash
EMAIL_CREDENTIALS='me@icloud.com:app-password,me@uni.edu:password:imap.uni.edu,me@corp.example:pw:localhost:1993'
```

Each is `address:password[:imap-host[:port]]`. Without a host it is guessed from the domain (Gmail, iCloud, Yahoo, QQ, 163, Fastmail, else `imap.<domain>`); the port defaults to 993 with TLS. Use app passwords where the provider has them. Outlook.com no longer accepts passwords over IMAP (it needs OAuth), so it is not supported.

## Run

Claude Code:

```bash
claude mcp add imap-draft -e EMAIL_CREDENTIALS=... -- imap-draft-mcp
```

Codex:

```bash
codex mcp add imap-draft --env EMAIL_CREDENTIALS=... -- imap-draft-mcp
```

Clients configured with JSON (Cursor, Claude Desktop and others):

```json
{
  "mcpServers": {
    "imap-draft": {
      "command": "imap-draft-mcp",
      "env": { "EMAIL_CREDENTIALS": "..." }
    }
  }
}
```

With Docker, the command is `docker run -i --rm -e EMAIL_CREDENTIALS ghcr.io/zedong-peng/imap-draft-mcp`.

### On a server

For agents running elsewhere, serve Streamable HTTP behind something that does authentication, such as `tailscale serve`:

```bash
imap-draft-mcp -http 127.0.0.1:8080 -allow-host server.tailnet.ts.net
tailscale serve --bg --https=8443 --set-path=/$(openssl rand -hex 16) http://127.0.0.1:8080/mcp
```

The server has no authentication of its own, so keep it on loopback. `-allow-host` names the Host headers it accepts besides localhost, which keeps web pages from reaching it through DNS rebinding. A hosted agent that can only start commands in its sandbox reaches it through [tailnet-mcp](https://github.com/zedong-peng/tailnet-mcp); [this example](https://github.com/zedong-peng/tailnet-mcp/tree/main/examples/private-tools) is a complete setup.

## Build and test

```bash
go test -race ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" .
```

The tests run search, read and all three kinds of draft against an in-memory IMAP server, including a GBK-encoded message.
