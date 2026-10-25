package server

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cbzr/internal/book"
)

func TestAuthorizeRejectsForeignHostAndToken(t *testing.T) {
	s := &Server{port: 54321, token: "secret"}
	handler := s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/b/0/" {
			t.Errorf("handler saw %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		url  string
		want int
	}{
		{"http://attacker.example:54321/secret/b/0/", http.StatusForbidden},
		{"http://127.0.0.1:54322/secret/b/0/", http.StatusForbidden},
		{"http://127.0.0.1:54321/b/0/", http.StatusNotFound},
		{"http://127.0.0.1:54321/guess/b/0/", http.StatusNotFound},
		{"http://127.0.0.1:54321/secret/b/0/", http.StatusNoContent},
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest("GET", tc.url, nil))
		if res.Code != tc.want {
			t.Fatalf("%s: %d, want %d", tc.url, res.Code, tc.want)
		}
	}
}

func TestOpenLinksWorkOnce(t *testing.T) {
	s := new(Server)
	defer s.Close()
	s.SetBooks([]*book.Book{pageBook(t, "page.png", nil)})
	stale, err := s.Open("b/0/?p=0")
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.Open("b/0/?p=0&invert=1")
	if err != nil || strings.Contains(link, s.token) || len(s.token) < 20 {
		t.Fatalf("link %q leaks the token or failed: %v", link, err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(url string) *http.Response {
		t.Helper()
		res, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := get(stale); res.StatusCode != http.StatusGone {
		t.Fatalf("a replaced link: HTTP %d", res.StatusCode)
	}
	res := get(link)
	target := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || target != "/"+s.token+"/b/0/?p=0&invert=1" {
		t.Fatalf("first use: HTTP %d to %q", res.StatusCode, target)
	}
	if res := get(link); res.StatusCode != http.StatusGone {
		t.Fatalf("second use: HTTP %d", res.StatusCode)
	}
	root, _, _ := strings.Cut(link, "/open/")
	for path, want := range map[string]int{target: http.StatusOK, "/": http.StatusNotFound} {
		if res := get(root + path); res.StatusCode != want {
			t.Fatalf("%s: HTTP %d, want %d", path, res.StatusCode, want)
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

func TestReaderRedirectKeepsRelativeLinks(t *testing.T) {
	s := new(Server)
	s.SetBooks([]*book.Book{pageBook(t, "page.png", nil)})
	res := httptest.NewRecorder()
	s.handleBook(res, httptest.NewRequest("GET", "/b/0?p=1", nil))
	// Relative to /TOKEN/b/0, "0/?p=1" keeps the token the mux never saw.
	if res.Code != http.StatusMovedPermanently || res.Header().Get("Location") != "0/?p=1" {
		t.Fatalf("redirect: %d %q", res.Code, res.Header().Get("Location"))
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
		if res.Code != http.StatusOK || !strings.Contains(body, want) || !strings.Contains(body, "invertible=[true]") {
			t.Fatalf("inversion query %q: HTTP %d, unexpected inversion settings", query, res.Code)
		}
	}
}

func TestReaderTemplateCarriesPerPageInversion(t *testing.T) {
	var page bytes.Buffer
	err := readerTmpl.Execute(&page, map[string]any{
		"Title": "Mixed EPUB", "Pages": 3, "Page": 0,
		"Inverted": true, "Invertible": []bool{true, false, true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(page.String()), ""), "invertible=[true,false,true]") {
		t.Fatal("reader lost per-page inversion eligibility")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("browser interaction check needs Node.js")
	}
	_, script, ok := strings.Cut(page.String(), "<script>")
	if !ok {
		t.Fatal("reader script missing")
	}
	script, _, _ = strings.Cut(script, "</script>")
	cmd := exec.Command(node, "-e", `
const assert=require('node:assert/strict'), vm=require('node:vm'), fs=require('node:fs');
const events={};let src='', filtered=false, requests=0, url='';
const img={complete:false,style:{},classList:{toggle(name,on){filtered=on;}},
getAttribute(){return src;},get src(){return src;},set src(value){src=value;this.complete=false;requests++;},
addEventListener(name,fn){events[name]=fn;}};
vm.runInNewContext(fs.readFileSync(0,'utf8'),{
document:{getElementById(id){return id==='pg'?img:{};}},Image:function(){},innerWidth:100,
history:{replaceState(state,title,value){url=value;}},addEventListener(name,fn){events[name]=fn;}});
const key=k=>events.keydown({key:k,preventDefault(){}});
const load=()=>{img.complete=true;events.load();};
assert.equal(filtered,true);assert.equal(img.style.visibility,'hidden');load();
key('i');assert.equal(filtered,false);assert.equal(requests,1);assert.equal(img.style.visibility,'visible');
key('i');assert.equal(filtered,true);
key('l');assert.equal(src,'page/1');assert.equal(filtered,false);assert.equal(img.style.visibility,'hidden');
load();assert.equal(img.style.visibility,'visible');assert.equal(filtered,false);
key('l');assert.equal(filtered,true);assert.equal(img.style.visibility,'hidden');
key('i');assert.equal(filtered,false);assert.equal(requests,3);load();
key('i');assert.equal(filtered,true);assert.equal(requests,3);assert.equal(url,'?p=2&invert=1');
events.click({clientX:0});assert.equal(filtered,false);load();
`)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser inversion: %v\n%s", err, output)
	}
}

func TestOwnerOnlyKnowsLoopbackOwners(t *testing.T) {
	// Two sockets of one loopback connection, as /proc/net/tcp lists them.
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:C350 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 101 1 0 20 4 30 10 -1
   1: 0100007F:D431 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1001        0 102 1 0 20 4 30 10 -1`
	if uid, ok := socketUID(table, "0100007F:D431", "0100007F:C350"); !ok || uid != 1001 {
		t.Fatalf("client owner = %d, %v", uid, ok)
	}
	if _, ok := socketUID(table, "0100007F:D432", "0100007F:C350"); ok {
		t.Fatal("found a socket that is not in the table")
	}
	// tcp6 lists an IPv4-mapped connection behind the ::ffff: prefix, in
	// host-order words; the fixture has a little-endian machine's.
	if binary.NativeEndian.Uint16([]byte{1, 0}) == 1 {
		table6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:C350 01 00000000:00000000 00:00000000 00000000  1002        0 103 1 0 20 4 30 10 -1`
		if uid, ok := socketUID(table6, mappedPrefix+"0100007F:D431", mappedPrefix+"0100007F:C350"); !ok || uid != 1002 {
			t.Fatalf("mapped client owner = %d, %v", uid, ok)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ownerOnly{ln}.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	c := <-accepted
	defer c.Close()
	if _, err := os.Stat("/proc/net/tcp"); err == nil {
		if uid, ok := peerUID(c); !ok || uid != os.Getuid() {
			t.Fatalf("own connection owner = %d, %v", uid, ok)
		}
	}
}

func TestOwnerOnlyClosesOtherUsersConnections(t *testing.T) {
	saved := peerUID
	t.Cleanup(func() { peerUID = saved })
	stranger := true
	peerUID = func(net.Conn) (int, bool) {
		if stranger {
			stranger = false
			return os.Getuid() + 1, true
		}
		return os.Getuid(), true
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := (ownerOnly{ln}).Accept(); err == nil {
			accepted <- c
		}
	}()
	other, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	if _, err := other.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("another user's connection stayed open: %v", err)
	}
	owner, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	c := <-accepted
	defer c.Close()
	if c.RemoteAddr().String() != owner.LocalAddr().String() {
		t.Fatal("Accept returned another user's connection")
	}
}
