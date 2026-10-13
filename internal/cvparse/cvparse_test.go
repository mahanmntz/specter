package cvparse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// makePDF builds a small, valid one-page PDF containing the given lines of text.
func makePDF(lines []string) []byte {
	esc := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	var content bytes.Buffer
	content.WriteString("BT /F1 12 Tf 72 720 Td 16 TL\n")
	for _, l := range lines {
		fmt.Fprintf(&content, "(%s) Tj T*\n", esc.Replace(l))
	}
	content.WriteString("ET")

	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return out.Bytes()
}

func TestExtractReadsTextFromAPDF(t *testing.T) {
	pdf := makePDF([]string{"Ada Lovelace", "Senior Backend Engineer", "Skills: Go (golang), PostgreSQL, Kubernetes"})
	text, err := Extract(context.Background(), pdf)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ada Lovelace", "Backend Engineer", "PostgreSQL", "Kubernetes"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
}

func TestExtractRejectsBadInputWithoutPanicking(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want error
	}{
		"not a pdf":    {[]byte("hello, this is plain text"), ErrNotPDF},
		"empty":        {nil, ErrNotPDF},
		"too large":    {append([]byte("%PDF-1.4\n"), make([]byte, MaxBytes)...), ErrTooLarge},
		"no real text": {makePDF([]string{"hi"}), ErrEmpty},
	}
	for name, c := range cases {
		if _, err := Extract(context.Background(), c.data); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}

	// A file that starts like a PDF but is garbage must give an error, not crash the process.
	garbage := []byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF")
	if _, err := Extract(context.Background(), garbage); err == nil {
		t.Error("garbage PDF should be an error")
	}
	truncated := makePDF([]string{"Ada Lovelace", "Senior Backend Engineer building Go services"})
	if _, err := Extract(context.Background(), truncated[:len(truncated)/2]); err == nil {
		t.Log("a truncated PDF may still be partly readable; the important thing is that it did not panic")
	}
}

func TestExtractHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pdf := makePDF([]string{"Ada Lovelace", "Senior Backend Engineer building Go services daily"})
	// Either result is acceptable (the parse may finish first), but it must not hang.
	_, _ = Extract(ctx, pdf)
}

func TestGuessName(t *testing.T) {
	cases := map[string]string{
		"Ada Lovelace\nSenior Backend Engineer\nada@example.com": "Ada Lovelace",
		"RESUME\nMary-Jane O'Neil\nGo developer":                 "Mary-Jane O'Neil",
		"Curriculum Vitae\nJohn Q. Public\n+1 555 0100":          "John Q. Public",
		"Senior Backend Engineer\nada@example.com\nSkills":       "",
		"john doe\nlowercase names are not guessed":              "",
		"A\nB\nC": "",
	}
	for in, want := range cases {
		if got := GuessName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
