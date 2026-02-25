package resumer

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Locator identifies which server owns the live response recording for a conversation.
type Locator struct {
	Host     string
	Port     int
	FileName string
}

func (l Locator) Encode() string {
	return fmt.Sprintf("%s|%d|%s", strings.TrimSpace(l.Host), l.Port, strings.TrimSpace(l.FileName))
}

func (l Locator) Address() string {
	host := strings.TrimSpace(l.Host)
	if host == "" {
		host = "localhost"
	}
	if l.Port > 0 {
		return net.JoinHostPort(host, strconv.Itoa(l.Port))
	}
	return host
}

func (l Locator) MatchesAddress(address string) bool {
	if strings.TrimSpace(address) == "" {
		return false
	}
	return sameAddress(l.Address(), address)
}

func DecodeLocator(raw string) (Locator, bool) {
	parts := strings.SplitN(strings.TrimSpace(raw), "|", 3)
	if len(parts) != 3 {
		return Locator{}, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		port = 0
	}
	return Locator{
		Host:     strings.TrimSpace(parts[0]),
		Port:     port,
		FileName: strings.TrimSpace(parts[2]),
	}, true
}

func locatorFromAddress(advertisedAddress string, fileName string) Locator {
	host := "localhost"
	port := 0

	addr := strings.TrimSpace(advertisedAddress)
	if addr != "" {
		if h, p, err := net.SplitHostPort(addr); err == nil {
			host = strings.TrimSpace(h)
			if parsed, convErr := strconv.Atoi(strings.TrimSpace(p)); convErr == nil {
				port = parsed
			}
		} else {
			host = addr
		}
	}

	if host == "" {
		host = "localhost"
	}

	return Locator{Host: host, Port: port, FileName: strings.TrimSpace(fileName)}
}

func sameAddress(a, b string) bool {
	na := normalizeAddress(a)
	nb := normalizeAddress(b)
	if na == nb {
		return true
	}

	ha, pa, errA := splitHostPort(na)
	hb, pb, errB := splitHostPort(nb)
	if errA != nil || errB != nil {
		return false
	}
	if pa != pb {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ha), strings.TrimSpace(hb))
}

func normalizeAddress(address string) string {
	return strings.TrimSpace(strings.ToLower(address))
}

func splitHostPort(address string) (string, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		return host, port, nil
	}
	if !strings.Contains(address, ":") {
		return "", "", err
	}
	idx := strings.LastIndex(address, ":")
	if idx <= 0 || idx >= len(address)-1 {
		return "", "", err
	}
	return address[:idx], address[idx+1:], nil
}
