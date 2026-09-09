package server

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cbzr/internal/book"
)

func TestAuthorizeRejectsForeignHost(t *testing.T) {
	s := &Server{port: 54321}
	handler := s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, host := range []string{"attacker.example:54321", "127.0.0.1:54322", "127.0.0.1:54321"} {
		req := httptest.NewRequest("GET", "http://"+host+"/", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		want := http.StatusForbidden
		if host == "127.0.0.1:54321" {
			want = http.StatusNoContent
		}
		if res.Code != want {
			t.Fatalf("host %q: %d, want %d", host, res.Code, want)
		}
	}
}

func pageBook(t *testing.T, name string, data []byte) *book.Book {
	t.Helper()
	path := filepath.Join(t.TempDir(), "comic.cbz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := book.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestPageURLsDoNotCacheReplacedBooks(t *testing.T) {
	s := new(Server)
	for _, width := range []int{1, 2} {
		var content bytes.Buffer
		if err := png.Encode(&content, image.NewRGBA(image.Rect(0, 0, width, 1))); err != nil {
			t.Fatal(err)
		}
		s.SetBooks([]*book.Book{pageBook(t, "page.png", content.Bytes())})
		res := httptest.NewRecorder()
		s.handleBook(res, httptest.NewRequest("GET", "/b/0/page/0", nil))
		if res.Code != http.StatusOK || res.Header().Get("Cache-Control") != "no-store" || !bytes.Equal(res.Body.Bytes(), content.Bytes()) {
			t.Fatalf("page: %d, %v", res.Code, res.Header())
		}
	}
}

func TestPageRejectsOversizedDimensions(t *testing.T) {
	data := make([]byte, 54)
	copy(data, "BM")
	binary.LittleEndian.PutUint32(data[10:], 54)
	binary.LittleEndian.PutUint32(data[14:], 40)
	binary.LittleEndian.PutUint32(data[18:], 50000)
	binary.LittleEndian.PutUint32(data[22:], 50000)
	binary.LittleEndian.PutUint16(data[26:], 1)
	binary.LittleEndian.PutUint16(data[28:], 24)
	s := new(Server)
	s.SetBooks([]*book.Book{pageBook(t, "page.bmp", data)})
	res := httptest.NewRecorder()
	s.handleBook(res, httptest.NewRequest("GET", "/b/0/page/0", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("oversized page: HTTP %d", res.Code)
	}
}

func TestReaderInversionQuery(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	s := new(Server)
	s.SetBooks([]*book.Book{pageBook(t, "page.png", data.Bytes())})
	for _, query := range []string{"", "0", "1", "true", "<script>"} {
		res := httptest.NewRecorder()
		s.handleBook(res, httptest.NewRequest("GET", "/b/0/?invert="+query, nil))
		want := "inverted=false;"
		if query == "1" {
			want = "inverted=true;"
		}
		body := strings.Join(strings.Fields(res.Body.String()), "")
		if res.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("inversion query %q: HTTP %d, missing %q", query, res.Code, want)
		}
	}
}
