package auth

import "strings"

// first takes the leftmost entry of a comma separated header value.
func first(value string) string {
	lead, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(lead)
}

// trimHost strips the port Go appends to RemoteAddr.
func trimHost(address string) string {
	if i := strings.LastIndex(address, ":"); i > strings.LastIndex(address, "]") {
		return address[:i]
	}
	return address
}
