package core

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Pending requests live as tags on the playground OU so that every client
// sees the same queue without a store. One tag per request:
//
//	key   playplace:req:<name>
//	value v3 <len>:<owner> <len>:<ttlHours> <len>:<budget> <len>:<requestedBy>
//	         <len>:<unixTime> <len>:<via> <len>:<purpose> <len>:<flags> <len>:<journeyID>
//
// Fields are length-prefixed rather than delimited so that no escaping is
// needed, because AWS tag values may only contain letters, numbers, spaces,
// and _ . : / = + - @. Every field is validated against that alphabet before
// it is written. flags is a set of letters: "o" means an admin overrode the
// TTL and budget ceilings, "a" means the request was approved and the record
// is kept until the account is placed, followed by approvedBy, approvedAt,
// and the provider's create request id.
const (
	RequestTagPrefix = "playplace:req:"
	requestVersion   = "v3"
)

// Request is an account request waiting for approval.
type Request struct {
	JourneyID   string        `json:"journey_id,omitempty"`
	Name        string        `json:"name"`
	Owner       string        `json:"owner"`
	TTL         time.Duration `json:"ttl"`
	BudgetUSD   float64       `json:"budget_usd"`
	RequestedBy string        `json:"requested_by"`
	RequestedAt time.Time     `json:"requested_at"`
	Via         string        `json:"via"` // web, cli, slack
	Purpose     string        `json:"purpose,omitempty"`

	OverrideLimits bool `json:"override_limits,omitempty"` // an admin allowed values above the ceilings

	// Set once approved. The record stays in the queue tags, no longer
	// pending, until the account lands in the OU, so the inventory can show
	// owner, budget, and expiry while the provider is still creating it.
	Approved        bool      `json:"approved,omitempty"`
	ApprovedBy      string    `json:"approved_by,omitempty"`
	ApprovedAt      time.Time `json:"approved_at,omitempty"`
	CreateRequestID string    `json:"create_request_id,omitempty"`
}

// Key is the OU tag key for this request.
func (r Request) Key() string { return RequestTagPrefix + r.Name }

// ExpiresAt is when the account would expire if approved now.
func (r Request) ExpiresAt(now time.Time) time.Time { return now.Add(r.TTL) }

// Validate checks every stored field against the tag rules.
func (r Request) Validate() error {
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	if utf8.RuneCountInString(r.Key()) > MaxTagKey {
		return fmt.Errorf("name %q is too long for a tag key", r.Name)
	}
	for field, v := range map[string]string{"owner": r.Owner, "requested by": r.RequestedBy, "via": r.Via} {
		if v == "" {
			return fmt.Errorf("%s is required", field)
		}
		if err := ValidateTagValue(field, v); err != nil {
			return err
		}
	}
	if err := ValidateTagValue("journey id", r.JourneyID); err != nil {
		return err
	}
	if err := ValidatePurpose(r.Purpose); err != nil {
		return err
	}
	if r.Approved {
		if err := ValidateTagValue("approved by", r.ApprovedBy); err != nil {
			return err
		}
	}
	if utf8.RuneCountInString(r.Encode()) > MaxTagValue {
		return fmt.Errorf("request record is longer than %d characters; shorten the purpose", MaxTagValue)
	}
	return nil
}

func field(s string) string { return strconv.Itoa(utf8.RuneCountInString(s)) + ":" + s }

// Encode renders the tag value.
func (r Request) Encode() string {
	flags := ""
	if r.OverrideLimits {
		flags += "o"
	}
	if r.Approved {
		flags += "a"
	}
	parts := []string{
		field(r.Owner),
		field(strconv.Itoa(int(math.Ceil(r.TTL.Hours())))), // whole hours, rounded up; a shorter TTL would decode as unset
		field(strconv.FormatFloat(r.BudgetUSD, 'f', -1, 64)),
		field(r.RequestedBy),
		field(strconv.FormatInt(r.RequestedAt.Unix(), 10)),
		field(r.Via),
		field(strings.TrimSpace(r.Purpose)),
		field(flags),
		field(r.JourneyID),
	}
	if r.Approved {
		parts = append(parts, field(r.ApprovedBy), field(strconv.FormatInt(r.ApprovedAt.Unix(), 10)), field(r.CreateRequestID))
	}
	return requestVersion + " " + strings.Join(parts, " ")
}

// DecodeRequest parses a tag key and value written by Encode. Values in the
// earlier v2 and pipe-delimited v1 forms are still read without inventing history identity.
func DecodeRequest(key, value string) (Request, error) {
	name, ok := strings.CutPrefix(key, RequestTagPrefix)
	if !ok || name == "" {
		return Request{}, fmt.Errorf("not a request tag: %s", key)
	}
	var parts []string
	var err error
	baseFields := 8
	switch {
	case strings.HasPrefix(value, requestVersion+" "), strings.HasPrefix(value, "v2 "):
		encoded := strings.TrimPrefix(value, "v2 ")
		if strings.HasPrefix(value, requestVersion+" ") {
			baseFields = 9
			encoded = strings.TrimPrefix(value, requestVersion+" ")
		}
		parts, err = splitFields(encoded, baseFields)
		if err == nil && strings.Contains(parts[7], "a") {
			parts, err = splitFields(encoded, baseFields+3)
		}
	case strings.HasPrefix(value, "v1|"):
		parts, err = splitV1(value)
	default:
		err = errors.New("unknown record version")
	}
	if err != nil {
		return Request{}, fmt.Errorf("request %s has an unreadable value: %w", name, err)
	}
	hours, err1 := strconv.Atoi(parts[1])
	budget, err2 := strconv.ParseFloat(parts[2], 64)
	at, err3 := strconv.ParseInt(parts[4], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return Request{}, fmt.Errorf("request %s has an unreadable value: %w", name, err)
	}
	r := Request{
		Name:        name,
		Owner:       parts[0],
		TTL:         time.Duration(hours) * time.Hour,
		BudgetUSD:   budget,
		RequestedBy: parts[3],
		RequestedAt: time.Unix(at, 0).UTC(),
		Via:         parts[5],
		Purpose:     parts[6],

		OverrideLimits: strings.Contains(parts[7], "o"),
		Approved:       strings.Contains(parts[7], "a"),
	}
	if baseFields == 9 {
		r.JourneyID = parts[8]
	}
	if r.Approved && len(parts) == baseFields+3 {
		r.ApprovedBy = parts[baseFields]
		if t, err := strconv.ParseInt(parts[baseFields+1], 10, 64); err == nil {
			r.ApprovedAt = time.Unix(t, 0).UTC()
		}
		r.CreateRequestID = parts[baseFields+2]
	}
	return r, nil
}

// splitFields reads n length-prefixed fields separated by single spaces.
func splitFields(s string, n int) ([]string, error) {
	runes := []rune(s)
	out := make([]string, 0, n)
	i := 0
	for len(out) < n {
		j := i
		for j < len(runes) && runes[j] >= '0' && runes[j] <= '9' {
			j++
		}
		if j == i || j >= len(runes) || runes[j] != ':' {
			return nil, fmt.Errorf("bad length prefix at field %d", len(out))
		}
		l, err := strconv.Atoi(string(runes[i:j]))
		if err != nil || l < 0 {
			return nil, fmt.Errorf("bad length prefix at field %d", len(out))
		}
		start := j + 1
		if l > len(runes)-start {
			return nil, fmt.Errorf("field %d runs past the end", len(out))
		}
		out = append(out, string(runes[start:start+l]))
		i = start + l
		if i < len(runes) && runes[i] == ' ' {
			i++
		}
	}
	return out, nil
}

// splitV1 reads the old pipe-delimited, URL-escaped form.
func splitV1(value string) ([]string, error) {
	parts := strings.Split(value, "|")
	if len(parts) != 8 && len(parts) != 9 {
		return nil, errors.New("wrong field count")
	}
	unesc := func(s string) string {
		s = strings.ReplaceAll(s, "+", " ")
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == '%' && i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteByte(s[i])
		}
		return b.String()
	}
	flags := ""
	if len(parts) == 9 {
		flags = parts[8]
	}
	return []string{unesc(parts[1]), parts[2], parts[3], unesc(parts[4]), parts[5], unesc(parts[6]), unesc(parts[7]), flags}, nil
}

// fill copies the request's facts onto an account derived from the
// provider's create status, which knows only the name.
func (r Request) fill(a *Account) {
	a.JourneyID = r.JourneyID
	a.Owner = r.Owner
	a.BudgetUSD = r.BudgetUSD
	a.Purpose = r.Purpose
	a.RequestedBy = r.RequestedBy
	a.ApprovedBy = r.ApprovedBy
	if !r.ApprovedAt.IsZero() {
		a.ExpiresAt = r.ApprovedAt.Add(r.TTL)
	}
}

// pendingAccount shows a request in the inventory alongside real accounts.
func pendingAccount(r Request, now time.Time) *Account {
	return &Account{
		JourneyID:   r.JourneyID,
		ID:          "req:" + r.Name,
		Name:        r.Name,
		Owner:       r.Owner,
		Status:      StatusPending,
		BudgetUSD:   r.BudgetUSD,
		CreatedAt:   r.RequestedAt,
		ExpiresAt:   r.ExpiresAt(now),
		RequestedBy: r.RequestedBy,
		Purpose:     r.Purpose,
		Managed:     true,

		TTL:            r.TTL,
		OverrideLimits: r.OverrideLimits,
	}
}
