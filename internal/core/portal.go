package core

import (
	"errors"
	"net/url"
	"strings"
)

// CheckAccessPortalURL checks the address of the AWS access portal start
// page. The web UI puts it in a link and the ready notice prints it, so it
// must be an absolute https address with a host and no user name or
// password. An empty value is allowed and means no link. The errors are
// plain sentences an operator can act on.
func CheckAccessPortalURL(raw string) error {
	if raw == "" {
		return nil
	}
	if strings.TrimSpace(raw) != raw {
		return errors.New("the address must not start or end with spaces")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("the address is not a valid URL")
	}
	switch {
	case !u.IsAbs():
		return errors.New("the address must be a full URL that starts with https://")
	case u.Scheme != "https":
		return errors.New("the address must start with https://")
	case u.Opaque != "" || u.Host == "" || u.Hostname() == "":
		return errors.New("the address must name a host, such as https://example.awsapps.com/start")
	case u.User != nil:
		return errors.New("the address must not hold a user name or password")
	}
	return nil
}
