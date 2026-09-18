package web

import "net/url"

func unescape(s string) (string, error) { return url.QueryUnescape(s) }
