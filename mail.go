package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	_ "github.com/emersion/go-message/charset" // decode GBK, ISO-8859-x etc.
	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

// dial connects and logs in. insecure is only for tests against a local plain-text server.
var insecure bool

func dial(a account) (*imapclient.Client, error) {
	opts := &imapclient.Options{
		Dialer:    &net.Dialer{Timeout: 20 * time.Second},
		TLSConfig: &tls.Config{ServerName: a.Host},
	}
	var c *imapclient.Client
	var err error
	if insecure {
		c, err = imapclient.DialInsecure(a.addr(), opts)
	} else {
		c, err = imapclient.DialTLS(a.addr(), opts)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: connect %s: %w", a.Email, a.addr(), err)
	}
	if err := c.Login(a.Email, a.Password).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("%s: login: %w", a.Email, err)
	}
	return c, nil
}

// with runs f on a fresh logged-in connection: one per tool call.
func with[T any](a account, f func(*imapclient.Client) (T, error)) (T, error) {
	c, err := dial(a)
	if err != nil {
		var zero T
		return zero, err
	}
	defer func() {
		c.Logout().Wait()
		c.Close()
	}()
	return f(c)
}

type summary struct {
	Account string `json:"account"`
	Folder  string `json:"folder"`
	UID     uint32 `json:"uid"`
	Date    string `json:"date"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Unread  bool   `json:"unread"`
}

type query struct {
	Folder string
	Text   string
	From   string
	Since  time.Time
	Unread bool
	Limit  int
}

// search returns the newest messages matching q, newest first.
func search(c *imapclient.Client, a account, q query) ([]summary, error) {
	if _, err := c.Select(q.Folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("%s: open %s: %w", a.Email, q.Folder, err)
	}
	crit := &imap.SearchCriteria{Since: q.Since}
	if q.Text != "" {
		crit.Text = []string{q.Text}
	}
	if q.From != "" {
		crit.Header = []imap.SearchCriteriaHeaderField{{Key: "From", Value: q.From}}
	}
	if q.Unread {
		crit.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	data, err := c.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("%s: search: %w", a.Email, err)
	}
	uids := data.AllUIDs()
	slices.Sort(uids)
	if len(uids) > q.Limit {
		uids = uids[len(uids)-q.Limit:]
	}
	if len(uids) == 0 {
		return nil, nil
	}
	msgs, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, Envelope: true, Flags: true, InternalDate: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("%s: fetch: %w", a.Email, err)
	}
	out := make([]summary, 0, len(msgs))
	for _, m := range msgs {
		s := summary{Account: a.Email, Folder: q.Folder, UID: uint32(m.UID), Date: m.InternalDate.Format(time.RFC3339), Unread: !slices.Contains(m.Flags, imap.FlagSeen)}
		if e := m.Envelope; e != nil {
			s.Subject, s.From = e.Subject, addrs(e.From)
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(x, y summary) int { return int(y.UID) - int(x.UID) })
	return out, nil
}

func addrs(list []imap.Address) string {
	s := make([]string, 0, len(list))
	for _, a := range list {
		if a.Name != "" {
			s = append(s, fmt.Sprintf("%s <%s>", a.Name, a.Addr()))
		} else {
			s = append(s, a.Addr())
		}
	}
	return strings.Join(s, ", ")
}

// fetchRaw returns a message's full RFC 822 source without marking it read.
func fetchRaw(c *imapclient.Client, a account, folder string, uid uint32) ([]byte, error) {
	if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("%s: open %s: %w", a.Email, folder, err)
	}
	section := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return nil, fmt.Errorf("%s: fetch uid %d: %w", a.Email, uid, err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("%s: no message with uid %d in %s", a.Email, uid, folder)
	}
	return msgs[0].FindBodySection(section), nil
}

type attachment struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Size     int    `json:"size"`
}

type message struct {
	MessageID   string       `json:"message_id"`
	References  []string     `json:"-"`
	Date        time.Time    `json:"date"`
	From        string       `json:"from"`
	ReplyTo     string       `json:"reply_to,omitempty"`
	To          string       `json:"to"`
	Cc          string       `json:"cc,omitempty"`
	Subject     string       `json:"subject"`
	Text        string       `json:"text"`
	Truncated   bool         `json:"truncated,omitempty"`
	Attachments []attachment `json:"attachments,omitempty"`
}

const maxText = 100_000

// parse reads the headers, the best text body (plain, else HTML stripped to text) and the attachment list.
func parse(raw []byte) (*message, error) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && r == nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}
	h := r.Header
	m := &message{Subject: headerText(h, "Subject")}
	m.MessageID, _ = h.MessageID()
	m.References, _ = h.MsgIDList("References")
	m.Date, _ = h.Date()
	m.From, m.ReplyTo, m.To, m.Cc = addrList(h, "From"), addrList(h, "Reply-To"), addrList(h, "To"), addrList(h, "Cc")
	var plain, htmlText string
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break // keep what we have from a malformed message
		}
		switch ph := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := ph.ContentType()
			body, _ := io.ReadAll(p.Body)
			switch {
			case ct == "text/plain" && plain == "":
				plain = string(body)
			case ct == "text/html" && htmlText == "":
				htmlText = stripHTML(string(body))
			}
		case *mail.AttachmentHeader:
			name, _ := ph.Filename()
			ct, _, _ := ph.ContentType()
			n, _ := io.Copy(io.Discard, p.Body)
			m.Attachments = append(m.Attachments, attachment{Filename: name, Type: ct, Size: int(n)})
		}
	}
	m.Text = strings.TrimSpace(plain)
	if m.Text == "" {
		m.Text = htmlText
	}
	if len(m.Text) > maxText {
		m.Text, m.Truncated = m.Text[:maxText], true
	}
	return m, nil
}

func headerText(h mail.Header, key string) string {
	if s, err := h.Text(key); err == nil {
		return s
	}
	return h.Get(key)
}

func addrList(h mail.Header, key string) string {
	list, err := h.AddressList(key)
	if err != nil {
		return h.Get(key)
	}
	s := make([]string, len(list))
	for i, a := range list {
		if s[i] = a.Address; a.Name != "" {
			s[i] = a.String()
		}
	}
	return strings.Join(s, ", ")
}

// stripHTML keeps the visible text of an HTML body, one block per line.
func stripHTML(s string) string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(collapseBlank(b.String()))
		case html.StartTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "head":
				skip++
			case "br", "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "table":
				b.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "head":
				if skip > 0 {
					skip--
				}
			case "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "table":
				b.WriteByte('\n')
			}
		case html.TextToken:
			if skip == 0 {
				b.WriteString(strings.Join(strings.Fields(string(z.Text())), " "))
				b.WriteByte(' ')
			}
		}
	}
}

func collapseBlank(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// appendDraft stores raw in the account's Drafts folder, flagged \Draft and \Seen.
func appendDraft(c *imapclient.Client, a account, raw []byte) (string, error) {
	folder, err := draftsFolder(c)
	if err != nil {
		return "", fmt.Errorf("%s: find Drafts: %w", a.Email, err)
	}
	cmd := c.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft, imap.FlagSeen}, Time: time.Now()})
	if _, err := cmd.Write(raw); err != nil {
		return "", fmt.Errorf("%s: save draft: %w", a.Email, err)
	}
	if err := cmd.Close(); err != nil {
		return "", fmt.Errorf("%s: save draft: %w", a.Email, err)
	}
	if _, err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("%s: save draft to %s: %w", a.Email, folder, err)
	}
	return folder, nil
}

// draftsFolder is the mailbox flagged \Drafts, else one with a usual name.
func draftsFolder(c *imapclient.Client) (string, error) {
	// Plain LIST: servers with SPECIAL-USE flag \Drafts there too, and some (iCloud) reject LIST ... RETURN.
	boxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		if slices.Contains(b.Attrs, imap.MailboxAttrDrafts) {
			return b.Mailbox, nil
		}
	}
	for _, name := range []string{"Drafts", "[Gmail]/Drafts", "Draft", "INBOX.Drafts", "草稿箱"} {
		for _, b := range boxes {
			if strings.EqualFold(b.Mailbox, name) {
				return b.Mailbox, nil
			}
		}
	}
	return "", fmt.Errorf("no Drafts folder")
}
