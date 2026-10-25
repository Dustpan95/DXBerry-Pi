package httpapi

import "net/http"

// registerPages mounts the pages that are one shell under many addresses. /radio/{name} is the
// VFO page: the same index.html as /, and the page script reads the name from the address.
func (s *server) registerPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /radio/{name}", s.radioPage)
}

// radioPage serves the app shell for a well-formed radio name. Whether the radio exists is the
// page's question to /api/radio/{name}; a malformed name is never a page.
func (s *server) radioPage(w http.ResponseWriter, r *http.Request) {
	if !reRadioName.MatchString(r.PathValue("name")) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.serveFile(w, r, "index.html")
}
