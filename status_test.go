package main

import "testing"

func TestStatusURLSharePrefersShareURL(t *testing.T) {
	w := watchOut{
		Mode:     string(modeShare),
		URL:      "http://127.0.0.1:52531/w/0cfb59c4?t=abc",
		ShareURL: "http://127.0.0.1:7331/s/TqcYKLEp",
		ShortID:  "TqcYKLEp",
	}
	if got := statusURL(w); got != w.ShareURL {
		t.Errorf("statusURL = %q, want share URL %q", got, w.ShareURL)
	}
	if got := statusShortID(w); got != "TqcYKLEp" {
		t.Errorf("statusShortID = %q, want TqcYKLEp", got)
	}
}

func TestStatusURLLocalUsesPreview(t *testing.T) {
	w := watchOut{
		Mode: string(modeLocal),
		URL:  "http://127.0.0.1:7821/w/abcd1234?t=tok",
	}
	if got := statusURL(w); got != w.URL {
		t.Errorf("statusURL = %q, want preview URL", got)
	}
	if got := statusShortID(w); got != "-" {
		t.Errorf("statusShortID = %q, want -", got)
	}
}
