// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/wittywilla/wittymodule/service"
)

// Routes adds WittyRigs' routes, as integration.json lists them (patterns without /http).
func Routes(mux *http.ServeMux) {
	h := func(pattern string, admin bool, f func(*App, http.ResponseWriter, *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			who := service.Who(r)
			if who.User <= 0 {
				writeErr(w, 401, "sign in first")
				return
			}
			if admin && !who.Admin {
				writeErr(w, 403, "only an administrator can do that")
				return
			}
			a, err := Get()
			if err != nil {
				writeErr(w, 503, "WittyRigs couldn't start: "+err.Error())
				return
			}
			f(a, w, r)
		})
	}
	h("GET /rigs", false, (*App).handleRigs)
	h("GET /rigs/find", true, (*App).handleRigFind)
	h("POST /rigs", true, (*App).handleRigAdd)
	h("PATCH /rigs/{id}", true, (*App).handleRigUpdate)
	h("DELETE /rigs/{id}", true, (*App).handleRigRemove)
	h("POST /rigs/{id}/pair", true, (*App).handleRigPair)
	h("GET /rigs/{id}/pair", true, (*App).handleRigPairStatus)
	h("DELETE /rigs/{id}/pair", true, (*App).handleRigPairCancel)
	h("POST /rigs/{id}/devices", true, (*App).handleRigDevicePIN)
	h("POST /rigs/{id}/sync", true, (*App).handleRigSync)
	h("GET /rigs/{id}/apps", true, (*App).handleRigApps)
	h("PUT /rigs/{id}/apps/{app}", true, (*App).handleRigAppShown)
	h("POST /rigs/{id}/wake", false, (*App).handleRigWake)
	h("POST /rigs/{id}/quit", false, (*App).handleRigQuit)
	h("POST /games/{id}/play", false, (*App).handleRigPlay)
	h("POST /games/{id}/start", false, (*App).handleRigStart)
	h("POST /games/{id}/stream", false, (*App).handleRigStream)
	h("GET /sessions/{sid}", false, (*App).handleSession)
	h("POST /sessions/{sid}/webrtc", false, (*App).handleWebRTC)
	h("DELETE /sessions/{sid}", false, (*App).handleSessionEnd)
	h("GET /local", true, (*App).handleLocal)
	h("PUT /local", true, (*App).handleLocalSet)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// readJSON reads a small JSON body; none at all is an empty one.
func readJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

func pathInt(r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return v, err == nil && v > 0
}

func now() int64 { return time.Now().Unix() }

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
