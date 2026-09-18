package core

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// AWS tag limits. Keys may be 128 characters and values 256, and both may
// only contain letters, numbers, spaces, and the characters _ . : / = + - @.
// Every free-text value we store must fit, so it is validated on the way in.
const (
	MaxTagKey          = 128
	MaxTagValue        = 256
	MaxTagsPerResource = 50  // also the most requests the OU queue can hold
	MaxPurpose         = 120 // leaves room for the rest of a request record
)

var tagValueRe = regexp.MustCompile(`^[\p{L}\p{N}\p{Z}_.:/=+\-@]*$`)

// ValidTagValue reports whether s may be stored as an AWS tag value.
func ValidTagValue(s string) bool {
	return utf8.RuneCountInString(s) <= MaxTagValue && tagValueRe.MatchString(s)
}

// ValidateTagValue explains why a field cannot be stored as a tag value.
func ValidateTagValue(field, s string) error {
	if utf8.RuneCountInString(s) > MaxTagValue {
		return fmt.Errorf("%s is longer than %d characters", field, MaxTagValue)
	}
	if !tagValueRe.MatchString(s) {
		return fmt.Errorf("%s may only contain letters, numbers, spaces, and _ . : / = + - @", field)
	}
	return nil
}

// ValidatePurpose applies the purpose length cap on top of the tag rules.
func ValidatePurpose(s string) error {
	if utf8.RuneCountInString(s) > MaxPurpose {
		return fmt.Errorf("purpose is longer than %d characters", MaxPurpose)
	}
	return ValidateTagValue("purpose", s)
}
