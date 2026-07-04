// Package server serves the current book over HTTP for browser reading.
package server

import (
	"fmt"
	"html/template"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cbzr/internal/book"
)

// Server exposes open books at http://127.0.0.1:<port>/b/<n>/.
type Server struct {
	mu    sync.Mutex
	books []*book.Book
	ln    net.Listener
	port  int
}

// New creates an idle server; Start binds it lazily.
func New() *Server { return &Server{} }

// SetBooks replaces the served book list (index = pane order).
func (s *Server) SetBooks(bs []*book.Book) {
	s.mu.Lock()
	s.books = bs
	s.mu.Unlock()
}

// Start binds a random port in [50000, 60000) and serves in the background.
// Idempotent: returns the existing port when already running.
func (s *Server) Start() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.port, nil
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ {
		port := 50000 + rng.Intn(10000)
		ln, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			s.port = port
			break
		}
	}
	if ln == nil {
		return 0, fmt.Errorf("no free port in 50000-59999: %w", err)
	}
	s.ln = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/b/", s.handleBook)
	go http.Serve(ln, mux) //nolint:errcheck // dies with the process
	return s.port, nil
}

// Port returns the bound port, or 0 when not running.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return 0
	}
	return s.port
}

// Close stops the listener.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	err := s.ln.Close()
	s.ln = nil
	return err
}

func (s *Server) snapshot() []*book.Book {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*book.Book(nil), s.books...)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	bs := s.snapshot()
	type item struct {
		N     int
		Title string
		Pages int
	}
	items := make([]item, 0, len(bs))
	for i, b := range bs {
		if b == nil {
			continue
		}
		items = append(items, item{N: i, Title: b.Title, Pages: b.Len()})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	indexTmpl.Execute(w, items) //nolint:errcheck
}

// handleBook routes /b/<n>/ (reader page) and /b/<n>/page/<p> (image bytes).
func (s *Server) handleBook(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// parts: ["b", n] or ["b", n, "page", p]
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	n, err := strconv.Atoi(parts[1])
	bs := s.snapshot()
	if err != nil || n < 0 || n >= len(bs) || bs[n] == nil {
		http.NotFound(w, r)
		return
	}
	b := bs[n]

	if len(parts) == 4 && parts[2] == "page" {
		p, err := strconv.Atoi(parts[3])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data, mime, err := b.PageBytes(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(data) //nolint:errcheck
		return
	}

	if len(parts) == 2 {
		page := 0
		if q := r.URL.Query().Get("p"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v >= 0 && v < b.Len() {
				page = v
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		readerTmpl.Execute(w, map[string]any{ //nolint:errcheck
			"N": n, "Title": b.Title, "Pages": b.Len(), "Page": page,
		})
		return
	}
	http.NotFound(w, r)
}

var indexTmpl = template.Must(template.New("index").Parse(`<!doctype html>
<meta charset="utf-8"><title>cbzr</title>
<style>
body{background:#111;color:#ddd;font:16px/1.6 system-ui;max-width:40rem;margin:3rem auto;padding:0 1rem}
a{color:#8cf;text-decoration:none} a:hover{text-decoration:underline}
</style>
<h1>cbzr</h1>
{{if not .}}<p>No books open.</p>{{end}}
<ul>{{range .}}<li><a href="/b/{{.N}}/">{{.Title}}</a> — {{.Pages}} pages</li>{{end}}</ul>`))

var readerTmpl = template.Must(template.New("reader").Parse(`<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
body{background:#111;color:#ddd;font:14px system-ui;margin:0;display:flex;flex-direction:column;height:100vh}
header{padding:.4rem .8rem;display:flex;gap:1rem;align-items:center;background:#1a1a1a}
header a{color:#8cf;text-decoration:none}
main{flex:1;display:flex;align-items:center;justify-content:center;overflow:hidden}
img{max-width:100%;max-height:100%;object-fit:contain}
</style>
<header>
  <a href="/">index</a><strong>{{.Title}}</strong>
  <span id="pos"></span>
  <span style="opacity:.6">h/l or ←/→ pages, g/G first/last</span>
</header>
<main><img id="pg" alt="page"></main>
<script>
const pages={{.Pages}}, n={{.N}};let p={{.Page}};
const img=document.getElementById('pg'),pos=document.getElementById('pos');
function show(){p=Math.max(0,Math.min(pages-1,p));img.src='/b/'+n+'/page/'+p;
pos.textContent=(p+1)+'/'+pages;history.replaceState(null,'','/b/'+n+'/?p='+p);
if(p+1<pages){(new Image()).src='/b/'+n+'/page/'+(p+1);}}
addEventListener('keydown',e=>{
if(e.key==='l'||e.key==='ArrowRight'||e.key===' ')p++;
else if(e.key==='h'||e.key==='ArrowLeft')p--;
else if(e.key==='g')p=0;else if(e.key==='G')p=pages-1;else return;
e.preventDefault();show();});
img.addEventListener('click',e=>{p+=(e.clientX>innerWidth/2)?1:-1;show();});
show();
</script>`))
