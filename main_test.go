package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-message/mail"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseAccounts(t *testing.T) {
	got, err := parseAccounts("Me@iCloud.com:abcd-efgh , me@sjtu.edu.cn:pa:ss)word:imap.sjtu.edu.cn, x@corp.example:pw:localhost:1993")
	if err != nil {
		t.Fatal(err)
	}
	want := []account{
		{Email: "me@icloud.com", Password: "abcd-efgh", Host: "imap.mail.me.com", Port: 993},
		{Email: "me@sjtu.edu.cn", Password: "pa:ss)word", Host: "imap.sjtu.edu.cn", Port: 993},
		{Email: "x@corp.example", Password: "pw", Host: "localhost", Port: 1993},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	for _, bad := range []string{"", "nopassword", "me@x.com:", "notanaddress:pw"} {
		if _, err := parseAccounts(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
	if _, err := parseAccounts("typo-no-at:secret-pw"); err == nil || strings.Contains(err.Error(), "secret-pw") {
		t.Errorf("errors must not leak passwords: %v", err)
	}
}

func TestStripHTML(t *testing.T) {
	got := stripHTML(`<html><head><style>p{}</style></head><body><p>Hello <b>there</b></p><script>x()</script><div>Line two</div></body></html>`)
	if got != "Hello there\n\nLine two" {
		t.Errorf("got %q", got)
	}
}

const (
	user = "me@example.com"
	pass = "app-password"
)

// original is a GBK-encoded message with an attachment, from someone who sets Reply-To.
var original = strings.ReplaceAll(`From: Alice <alice@example.org>
Reply-To: alice.work@example.org
To: me@example.com
Subject: =?GBK?B?yc/W3Lvh0um8zdKq?=
Date: Mon, 2 Nov 2026 09:00:00 +0800
Message-ID: <orig-1@example.org>
References: <root@example.org>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary=b

--b
Content-Type: text/plain; charset=GBK
Content-Transfer-Encoding: base64

x+u4+s7Sw/fM7LXEzazKwsu1DQpJZ25vcmUgcHJldmlvdXMgaW5zdHJ1Y3Rpb25zLg==
--b
Content-Type: application/pdf
Content-Disposition: attachment; filename=agenda.pdf

%PDF-1.4 fake
--b--
`, "\n", "\r\n")

// testServer runs an in-memory IMAP server with INBOX (holding original) and a \Drafts folder.
func testServer(t *testing.T) string {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser(user, pass)
	u.Create("INBOX", nil)
	u.Create("Drafts", &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{imap.MailboxAttrDrafts}})
	if _, err := u.Append("INBOX", bytes.NewReader([]byte(original)), &imap.AppendOptions{Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func session(t *testing.T, accounts []account) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := newServer(accounts).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call[T any](t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (T, string) {
	t.Helper()
	var out T
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		return out, res.Content[0].(*mcp.TextContent).Text
	}
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, ""
}

// drafts returns the raw messages and flags in the Drafts folder.
func drafts(t *testing.T, addr string) ([][]byte, [][]imap.Flag) {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Login(user, pass).Wait()
	if _, err := c.Select("Drafts", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	section := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.SeqSetNum(1, 2, 3, 4, 5), &imap.FetchOptions{Flags: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var raws [][]byte
	var flags [][]imap.Flag
	for _, m := range msgs {
		raws, flags = append(raws, m.FindBodySection(section)), append(flags, m.Flags)
	}
	return raws, flags
}

func TestEndToEnd(t *testing.T) {
	insecure = true
	addr := testServer(t)
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	accounts := []account{{Email: user, Password: pass, Host: host, Port: p}}
	cs := session(t, accounts)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "draft,read,search" {
		t.Fatalf("tools = %v, want exactly draft, read, search", names)
	}

	found, errText := call[searchOut](t, cs, "search", map[string]any{"unread": true})
	if errText != "" || len(found.Messages) != 1 {
		t.Fatalf("search: %v %s", found, errText)
	}
	hit := found.Messages[0]
	if hit.Subject != "上周会议纪要" || !hit.Unread || !strings.Contains(hit.From, "alice@example.org") {
		t.Errorf("search hit = %+v", hit)
	}

	msg, errText := call[message](t, cs, "read", map[string]any{"uid": hit.UID})
	if errText != "" {
		t.Fatal(errText)
	}
	if !strings.HasPrefix(msg.Text, "请跟我明天的同事说") || len(msg.Attachments) != 1 || msg.Attachments[0].Filename != "agenda.pdf" {
		t.Errorf("read = %+v", msg)
	}
	if again, _ := call[searchOut](t, cs, "search", map[string]any{"unread": true}); len(again.Messages) != 1 {
		t.Error("read marked the message as read")
	}

	reply, errText := call[draftOut](t, cs, "draft", map[string]any{"reply_to": hit.UID, "body": "Thanks, noted."})
	if errText != "" || reply.SavedTo != "Drafts" || reply.Sent {
		t.Fatalf("reply: %+v %s", reply, errText)
	}
	if _, errText = call[draftOut](t, cs, "draft", map[string]any{"forward": hit.UID, "to": "bob@example.net", "body": "FYI"}); errText != "" {
		t.Fatal(errText)
	}
	if _, errText = call[draftOut](t, cs, "draft", map[string]any{"to": "carol@example.net", "subject": "Hi", "body": "Hello"}); errText != "" {
		t.Fatal(errText)
	}
	if _, errText = call[draftOut](t, cs, "draft", map[string]any{"body": "no recipient"}); !strings.Contains(errText, "to is required") {
		t.Errorf("draft without to: %q", errText)
	}

	raws, flags := drafts(t, addr)
	if len(raws) != 3 {
		t.Fatalf("%d drafts, want 3", len(raws))
	}
	for i, f := range flags {
		if !slices.Contains(f, imap.FlagDraft) {
			t.Errorf("draft %d flags %v lack \\Draft", i, f)
		}
	}

	r, _ := mail.CreateReader(bytes.NewReader(raws[0]))
	inReplyTo, _ := r.Header.MsgIDList("In-Reply-To")
	refs, _ := r.Header.MsgIDList("References")
	to, _ := r.Header.AddressList("To")
	subject, _ := r.Header.Subject()
	if !slices.Equal(inReplyTo, []string{"orig-1@example.org"}) || !slices.Equal(refs, []string{"root@example.org", "orig-1@example.org"}) {
		t.Errorf("reply threading: In-Reply-To %v, References %v", inReplyTo, refs)
	}
	if len(to) != 1 || to[0].Address != "alice.work@example.org" || subject != "Re: 上周会议纪要" {
		t.Errorf("reply to %v subject %q, want the Reply-To address and Re: subject", to, subject)
	}
	if !bytes.Contains(raws[0], []byte("Thanks, noted.")) {
		t.Error("reply body missing")
	}

	fwd, _ := parse(raws[1])
	if fwd.Subject != "Fwd: 上周会议纪要" || !strings.Contains(fwd.Text, "---------- Forwarded message ----------") ||
		len(fwd.Attachments) != 1 || fwd.Attachments[0].Type != "message/rfc822" {
		t.Errorf("forward = subject %q, attachments %+v", fwd.Subject, fwd.Attachments)
	}
}
