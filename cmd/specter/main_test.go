package main

import "testing"

func TestDetectATSTypeAndBoard(t *testing.T) {
	cases := []struct{ in, svc, board string }{
		{"boards.greenhouse.io/stripe", "greenhouse", "stripe"},
		{"jobs.lever.co/acme", "lever", "acme"},
		{"jobs.ashbyhq.com/acme", "ashby", "acme"},
		{"https://apply.workable.com/acme/", "workable", "acme"},
		{"careers.smartrecruiters.com/Acme", "smartrecruiters", "Acme"},
		{"https://acme.recruitee.com", "recruitee", "acme"},
		{"example.com", "generic", "example.com"},
	}
	for _, c := range cases {
		svc, board := detectATSTypeAndBoard(c.in)
		if svc != c.svc || board != c.board {
			t.Errorf("%s: got (%s,%s) want (%s,%s)", c.in, svc, board, c.svc, c.board)
		}
	}
}
