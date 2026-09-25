package http

import "strings"

type EmailAllowlist map[string]struct{}

func (a EmailAllowlist) Empty() bool { return len(a) == 0 }

func NewEmailAllowlist(emails []string) EmailAllowlist {
	allowlist := make(EmailAllowlist, len(emails))
	for _, email := range emails {
		normalized := strings.ToLower(strings.TrimSpace(email))
		if normalized != "" {
			allowlist[normalized] = struct{}{}
		}
	}
	return allowlist
}

func (a EmailAllowlist) Allows(email string) bool {
	_, ok := a[strings.ToLower(strings.TrimSpace(email))]
	return ok
}
