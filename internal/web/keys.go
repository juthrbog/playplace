package web

// keyLine is one line of the key list: the keys and what they do.
type keyLine struct {
	Keys []string
	Does string
}

// keyList is the list of the keys that act on the page of pg. app.js
// holds the keys. The home page moves through the queue, the accounts and
// the Needs attention ledger, and only an approver decides with keys or
// opens a request with Enter: a row of Your requests has no action for
// Enter. The accounts page has the search. ? works on every page with a
// viewer while the keys are on.
func keyList(pg page) []keyLine {
	var out []keyLine
	home := pg.Polls && pg.Path == "/"
	accounts := pg.Polls && pg.Path == pathAccounts
	if accounts {
		out = append(out, keyLine{[]string{"/"}, "Go to the search."})
	}
	if home || accounts {
		out = append(out, keyLine{[]string{"j", "k"}, "Go to the next or the previous row of a list."})
	}
	if home && pg.Viewer.Approver {
		out = append(out, keyLine{[]string{"Enter"}, "Open the request, or follow the link of the row."})
	} else if home {
		out = append(out, keyLine{[]string{"Enter"}, "Follow the link of the row."})
	} else if accounts {
		out = append(out, keyLine{[]string{"Enter"}, "Open the account of the row."})
	}
	if home && pg.Viewer.Approver {
		out = append(out,
			keyLine{[]string{"x"}, "Select the request, or clear it."},
			keyLine{[]string{"a"}, "Approve the selected requests, or else the request in focus."},
			keyLine{[]string{"d"}, "Deny the selected requests, or else the request in focus. A dialog asks for the reason."},
		)
	}
	return append(out, keyLine{[]string{"?"}, "Show this list."})
}
