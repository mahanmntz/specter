package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// minimalPDF builds a small valid one-page PDF with the given text lines.
func minimalPDF(lines []string) []byte {
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

func upload(t *testing.T, h http.Handler, field string, data []byte, withHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile(field, "cv.pdf")
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/cv/import", &body)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if withHeader {
		req.Header.Set("X-Specter", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCVImportProposesNameRoleAndSkills(t *testing.T) {
	pdf := minimalPDF([]string{
		"Ada Lovelace", "Senior Backend Engineer",
		"Built Go services on Kubernetes with PostgreSQL. Led the Go migration; designed gRPC APIs in Go.",
		"Skills", "Go, Python, PostgreSQL, Kubernetes, Docker",
	})
	h := newServer(newFake()).Handler()
	rec := upload(t, h, "file", pdf, true)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Source string
		Name   string
		Role   string
		Roles  []string
		Skills []importSkill
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Source != "cv" || out.Name != "Ada Lovelace" || out.Role != "backend" {
		t.Errorf("out = %+v", out)
	}
	sel := map[string]bool{}
	for _, s := range out.Skills {
		if s.Selected {
			sel[s.Name] = true
		}
	}
	for _, want := range []string{"Go", "PostgreSQL", "Kubernetes"} {
		if !sel[want] {
			t.Errorf("%s should be pre-selected: %+v", want, out.Skills)
		}
	}
}

func TestCVImportValidation(t *testing.T) {
	h := newServer(newFake()).Handler()
	cases := []struct {
		name   string
		field  string
		data   []byte
		header bool
		want   int
	}{
		{"missing X-Specter header", "file", minimalPDF([]string{"a b c d e f g"}), false, http.StatusForbidden},
		{"wrong field name", "document", minimalPDF([]string{"a b c d e f g"}), true, http.StatusBadRequest},
		{"not a pdf", "file", []byte("just some text"), true, http.StatusBadRequest},
		{"no extractable text", "file", minimalPDF([]string{"hi"}), true, http.StatusUnprocessableEntity},
		{"too large", "file", append([]byte("%PDF-1.4\n"), make([]byte, 6<<20)...), true, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if rec := upload(t, h, c.field, c.data, c.header); rec.Code != c.want {
			t.Errorf("%s → %d (%s), want %d", c.name, rec.Code, strings.TrimSpace(rec.Body.String()), c.want)
		}
	}

	// A non-multipart request is a client error, not a crash.
	req := httptest.NewRequest(http.MethodPost, "/api/cv/import", strings.NewReader("{}"))
	req.Host = "localhost"
	req.Header.Set("X-Specter", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-multipart → %d", rec.Code)
	}
}
