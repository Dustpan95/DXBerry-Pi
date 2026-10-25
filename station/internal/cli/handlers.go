package cli

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Register mounts POST /api/cli/{command}. The session gate is httpapi's.
func (c *CLI) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/cli/{command}", c.handle)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (c *CLI) handle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
		return
	}
	call := Call{Command: r.PathValue("command"), Args: body.Args, Stdin: body.Stdin}
	start := time.Now()
	res, err := c.Run(r.Context(), call)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.Logf("%s", logLine(call, res.Exit, time.Since(start)))
	writeJSON(w, http.StatusOK, map[string]any{"exit": res.Exit, "stdout": res.Stdout, "stderr": res.Stderr})
}

// logLine is "cli <command> <args> exit=N ms=M". A call that brought standard input logs only
// its first argument (the subcommand): the rest may sit next to a secret, and stdin never appears.
func logLine(call Call, exit int, d time.Duration) string {
	args := call.Args
	if call.Stdin != "" && len(args) > 1 {
		args = append(args[:1:1], "[args omitted: stdin]")
	}
	parts := append([]string{"cli", call.Command}, args...)
	parts = append(parts, "exit="+strconv.Itoa(exit), "ms="+strconv.FormatInt(d.Milliseconds(), 10))
	return strings.Join(parts, " ")
}
