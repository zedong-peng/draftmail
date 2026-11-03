package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

type draftSpec struct {
	From    string
	To      string
	Cc      string
	Subject string
	Body    string

	InReplyTo  string   // Message-ID being answered
	References []string // thread chain
	Attach     []byte   // forwarded original, attached as message/rfc822
}

// replySpec answers orig: to the Reply-To (else From) address unless to is set, "Re:" subject,
// threading headers, and the original quoted below the body.
func replySpec(from string, orig *message, to, cc, subject, body string) draftSpec {
	if to == "" {
		to = orig.ReplyTo
		if to == "" {
			to = orig.From
		}
	}
	if subject == "" {
		subject = prefixed("Re:", orig.Subject)
	}
	refs := orig.References
	if orig.MessageID != "" {
		refs = append(append([]string{}, refs...), orig.MessageID)
	}
	quoted := "> " + strings.ReplaceAll(strings.TrimSpace(orig.Text), "\n", "\n> ")
	return draftSpec{
		From: from, To: to, Cc: cc, Subject: subject,
		Body:       fmt.Sprintf("%s\n\nOn %s, %s wrote:\n%s\n", body, orig.Date.Format("Mon, 2 Jan 2006 at 15:04"), orig.From, quoted),
		InReplyTo:  orig.MessageID,
		References: refs,
	}
}

// forwardSpec passes orig on: "Fwd:" subject, its headers and text below the body, and, when it
// has attachments, the whole original attached so nothing is lost.
func forwardSpec(from string, orig *message, raw []byte, to, cc, subject, body string) draftSpec {
	if subject == "" {
		subject = prefixed("Fwd:", orig.Subject)
	}
	block := fmt.Sprintf("---------- Forwarded message ----------\nFrom: %s\nDate: %s\nSubject: %s\nTo: %s\n",
		orig.From, orig.Date.Format(time.RFC1123Z), orig.Subject, orig.To)
	if orig.Cc != "" {
		block += "Cc: " + orig.Cc + "\n"
	}
	s := draftSpec{From: from, To: to, Cc: cc, Subject: subject, Body: fmt.Sprintf("%s\n\n%s\n%s\n", body, block, orig.Text)}
	if len(orig.Attachments) > 0 {
		s.Attach = raw
	}
	return s
}

func prefixed(prefix, subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), strings.ToLower(prefix)) {
		return subject
	}
	return prefix + " " + subject
}

// compose renders the draft as an RFC 5322 message.
func compose(s draftSpec, now time.Time) ([]byte, error) {
	var h mail.Header
	h.SetDate(now)
	h.SetSubject(s.Subject)
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	h.SetAddressList("From", []*mail.Address{from})
	for key, value := range map[string]string{"To": s.To, "Cc": s.Cc} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		list, err := mail.ParseAddressList(value)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", strings.ToLower(key), value, err)
		}
		h.SetAddressList(key, list)
	}
	h.SetMessageID(messageID(from.Address))
	if s.InReplyTo != "" {
		h.SetMsgIDList("In-Reply-To", []string{s.InReplyTo})
	}
	if len(s.References) > 0 {
		h.SetMsgIDList("References", s.References)
	}

	var buf bytes.Buffer
	text := func(w io.Writer) error { _, err := io.WriteString(w, s.Body); return err }
	if s.Attach == nil {
		h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		w, err := mail.CreateSingleInlineWriter(&buf, h)
		if err != nil {
			return nil, err
		}
		if err := text(w); err != nil {
			return nil, err
		}
		return buf.Bytes(), w.Close()
	}
	mw, err := mail.CreateWriter(&buf, h)
	if err != nil {
		return nil, err
	}
	var ih mail.InlineHeader
	ih.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	w, err := mw.CreateSingleInline(ih)
	if err != nil {
		return nil, err
	}
	if err := text(w); err != nil {
		return nil, err
	}
	w.Close()
	var ah mail.AttachmentHeader
	ah.SetContentType("message/rfc822", nil)
	ah.SetFilename("forwarded.eml")
	aw, err := mw.CreateAttachment(ah)
	if err != nil {
		return nil, err
	}
	if _, err := aw.Write(s.Attach); err != nil {
		return nil, err
	}
	aw.Close()
	return buf.Bytes(), mw.Close()
}

func messageID(from string) string {
	b := make([]byte, 12)
	rand.Read(b)
	domain := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		domain = from[i+1:]
	}
	return hex.EncodeToString(b) + "@" + domain
}
