// Package cvparse extracts text from a CV in PDF format, entirely in memory on the local machine.
package cvparse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

// MaxBytes is the largest PDF accepted.
const MaxBytes = 5 << 20

// maxText bounds how much extracted text is analysed.
const maxText = 1 << 20

// Errors a caller can show to the user.
var (
	ErrNotPDF    = errors.New("that file is not a PDF")
	ErrEmpty     = errors.New("no text found in that PDF (is it a scan? export it with selectable text instead)")
	ErrEncrypted = errors.New("that PDF is password protected")
	ErrTooLarge  = errors.New("that PDF is too large (5 MB maximum)")
	ErrTimeout   = errors.New("reading that PDF took too long")
)

// Extract returns the plain text of a PDF. Malformed files cannot crash the caller, and parsing
// is abandoned after a timeout.
func Extract(ctx context.Context, data []byte) (string, error) {
	if len(data) > MaxBytes {
		return "", ErrTooLarge
	}
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	if !bytes.Contains(head, []byte("%PDF-")) {
		return "", ErrNotPDF
	}

	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil { // the parser panics on some malformed files
				done <- result{err: fmt.Errorf("could not read that PDF")}
			}
		}()
		text, err := extract(data)
		done <- result{text, err}
	}()

	select {
	case r := <-done:
		return r.text, r.err
	case <-ctx.Done():
		return "", ErrTimeout
	case <-time.After(20 * time.Second):
		return "", ErrTimeout
	}
}

func extract(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "encrypt") || strings.Contains(strings.ToLower(err.Error()), "password") {
			return "", ErrEncrypted
		}
		return "", fmt.Errorf("could not read that PDF")
	}
	rd, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("could not read that PDF")
	}
	buf, err := io.ReadAll(io.LimitReader(rd, maxText))
	if err != nil && len(buf) == 0 {
		return "", fmt.Errorf("could not read that PDF")
	}
	text := strings.TrimSpace(string(buf))
	if len(strings.Fields(text)) < 5 {
		return "", ErrEmpty
	}
	return text, nil
}

var (
	notNameWords = regexp.MustCompile(`(?i)\b(resume|curriculum|vitae|cv|engineer|developer|software|senior|junior|manager|summary|profile|contact|email|phone|experience|skills|education)\b`)
	nameLine     = regexp.MustCompile(`^[\p{Lu}][\p{L}'’.-]+(\s+[\p{Lu}][\p{L}'’.-]+){1,3}$`)
)

// GuessName returns the person's name when one of the first lines of the CV looks like one.
func GuessName(text string) string {
	seen := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if seen++; seen > 8 {
			break
		}
		if len(line) > 40 || strings.ContainsAny(line, "@:/0123456789|•") || notNameWords.MatchString(line) {
			continue
		}
		if nameLine.MatchString(line) {
			return line
		}
	}
	return ""
}
