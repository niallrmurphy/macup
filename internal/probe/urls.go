package probe

import (
	"errors"
	"net/url"
)

func parseHTTPS(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("not a public HTTPS URL")
	}
	return u, nil
}
