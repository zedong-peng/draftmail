package main

import (
	"fmt"
	"strconv"
	"strings"
)

type account struct {
	Email    string
	Password string
	Host     string // IMAP host
	Port     int
}

func (a account) addr() string { return fmt.Sprintf("%s:%d", a.Host, a.Port) }

// parseAccounts reads EMAIL_CREDENTIALS: comma-separated `address:password[:imap-host[:port]]`.
// A password may contain ':' when the host is given, since the host is recognized by its dots.
func parseAccounts(s string) ([]account, error) {
	var out []account
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) < 2 || !strings.Contains(parts[0], "@") {
			return nil, fmt.Errorf("credential %q: want address:password[:imap-host[:port]]", redact(entry))
		}
		a := account{Email: strings.ToLower(parts[0]), Port: 993}
		rest := parts[1:]
		if n := len(rest); n >= 3 && isHost(rest[n-2]) {
			if p, err := strconv.Atoi(rest[n-1]); err == nil {
				a.Host, a.Port, rest = rest[n-2], p, rest[:n-2]
			}
		}
		if n := len(rest); a.Host == "" && n >= 2 && isHost(rest[n-1]) {
			a.Host, rest = rest[n-1], rest[:n-1]
		}
		a.Password = strings.Join(rest, ":")
		if a.Password == "" {
			return nil, fmt.Errorf("credential for %s: empty password", a.Email)
		}
		if a.Host == "" {
			a.Host = imapHost(a.Email)
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("EMAIL_CREDENTIALS is empty")
	}
	return out, nil
}

func isHost(s string) bool { return s == "localhost" || strings.Contains(s, ".") }

// imapHost guesses the IMAP server from the address's domain.
func imapHost(email string) string {
	domain := email[strings.LastIndex(email, "@")+1:]
	switch domain {
	case "gmail.com", "googlemail.com":
		return "imap.gmail.com"
	case "icloud.com", "me.com", "mac.com":
		return "imap.mail.me.com"
	case "yahoo.com", "ymail.com":
		return "imap.mail.yahoo.com"
	case "qq.com", "foxmail.com":
		return "imap.qq.com"
	case "163.com":
		return "imap.163.com"
	case "fastmail.com":
		return "imap.fastmail.com"
	}
	return "imap." + domain
}

func redact(entry string) string {
	if i := strings.Index(entry, ":"); i >= 0 {
		return entry[:i] + ":***"
	}
	return entry
}

// pick finds the account named by email, or the only account when email is empty.
func pick(accounts []account, email string) (account, error) {
	if email == "" {
		if len(accounts) == 1 {
			return accounts[0], nil
		}
		return account{}, fmt.Errorf("several accounts are configured, pass account: %s", list(accounts))
	}
	for _, a := range accounts {
		if strings.EqualFold(a.Email, email) {
			return a, nil
		}
	}
	return account{}, fmt.Errorf("no account %q; configured: %s", email, list(accounts))
}

func list(accounts []account) string {
	names := make([]string, len(accounts))
	for i, a := range accounts {
		names[i] = a.Email
	}
	return strings.Join(names, ", ")
}
