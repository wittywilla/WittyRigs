// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/wittywilla/wittycouch/gamestream"
	"github.com/wittywilla/wittycouch/relay"
	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/service"
)

// Playing a rig's game in the page: the Moonlight host program (the wittycouchhost module) talks
// to the PC, and the stream reaches the browser over WebRTC. It uses this server's own pairing,
// and the same checks as any other way of playing it.

// streamSettings are the module's settings for streams (sdui.json, set by an administrator in
// every app's Settings, Modules), as the server says them; their defaults where it can't.
type streamSettings struct {
	MaxStreams int    `json:"maxStreams"`
	UDPPort    int    `json:"udpPort"`
	PublicIP   string `json:"publicIp"`
	STUN       string `json:"stun"`
}

func (a *App) streamSettings(ctx context.Context) streamSettings {
	st := streamSettings{MaxStreams: 2}
	vals, err := a.Host.Settings(ctx, host.On{})
	if err != nil {
		return st
	}
	if b, err := json.Marshal(vals); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.MaxStreams < 1 {
		st.MaxStreams = 2
	}
	return st
}

// stream is one stream, and who it belongs to.
type stream struct {
	ID      string
	User    int64
	RigID   int64
	AppID   int
	Title   string
	Client  *gamestream.Rig
	Session *relay.Session
}

// playRequest is what a device wants. Numbers are limited to what's sensible, as a person's
// request ends up deciding what an encoder on someone's PC does.
type playRequest struct {
	Width   int `json:"width"`
	Height  int `json:"height"`
	FPS     int `json:"fps"`
	Bitrate int `json:"bitrate"`
}

func (q *playRequest) clean() {
	clamp := func(v, lo, hi, def int) int {
		if v == 0 {
			return def
		}
		return max(lo, min(hi, v))
	}
	q.Width = clamp(q.Width, 640, 3840, 1920) &^ 1
	q.Height = clamp(q.Height, 360, 2160, 1080) &^ 1
	q.FPS = clamp(q.FPS, 24, 120, 60)
	q.Bitrate = clamp(q.Bitrate, 1000, 150000, 20000)
}

var rtspURL = regexp.MustCompile(`^(rtsp|rtspenc)://[^/]*:([0-9]{1,5})/?$`)

// cleanSessionURL rebuilds where the stream connects from what the PC said. The host program
// connects where this says, and a far end has no business naming somewhere else: only its
// scheme and port are used, with the address already checked.
func cleanSessionURL(raw, ip string) (string, error) {
	m := rtspURL.FindStringSubmatch(raw)
	if m == nil {
		return "", errors.New("the PC gave a stream address that doesn't look right")
	}
	port, err := strconv.Atoi(m[2])
	if err != nil || port < 1024 || port > 65535 {
		return "", errors.New("the PC gave a stream address that doesn't look right")
	}
	return m[1] + "://" + net.JoinHostPort(ip, m[2]), nil
}

// resolveAllowed turns an address into the IP the host program should connect to, which is
// checked here, as that program connects where it's told.
func resolveAllowed(ctx context.Context, address string, policy gamestream.Policy) (string, error) {
	if ip, err := netip.ParseAddr(address); err == nil {
		return ip.String(), policy.Allow(ip)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", address)
	if err != nil || len(ips) == 0 {
		return "", gamestream.ErrNoAnswer
	}
	for _, ip := range ips {
		if err := policy.Allow(ip); err != nil {
			return "", err
		}
	}
	return ips[0].Unmap().String(), nil
}

var (
	errFull     = errors.New("This server is already streaming as many games as it's set to. Try again when one has finished.")
	errStarting = errors.New("Your game is already starting. Give it a moment.")
)

// startStream starts a game and the stream of it, for one person.
func (a *App) startStream(ctx context.Context, user int64, client *gamestream.Rig, appID int, title string, q playRequest, rigID int64) (*stream, error) {
	program, args, err := a.program(ctx)
	if err != nil {
		return nil, relay.ErrNoProgram
	}
	set := a.streamSettings(ctx)
	limit := set.MaxStreams
	a.mu.Lock()
	var mine []*stream
	for _, st := range a.streams {
		if st.User == user {
			mine = append(mine, st)
		}
	}
	others := len(a.streams) - len(mine) + len(a.starting)
	if a.starting[user] {
		a.mu.Unlock()
		return nil, errStarting
	}
	if others >= limit {
		a.mu.Unlock()
		return nil, errFull
	}
	a.starting[user] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.starting, user)
		a.mu.Unlock()
	}()
	info, err := client.Status(ctx)
	if err != nil {
		return nil, err
	}
	ip, err := resolveAllowed(ctx, client.Address, client.Policy)
	if err != nil {
		return nil, err
	}
	o := gamestream.StreamOptions{AppID: appID, Width: q.Width, Height: q.Height, FPS: q.FPS}
	if _, err := rand.Read(o.Key[:]); err != nil {
		return nil, err
	}
	if _, err := rand.Read(o.IV[:]); err != nil {
		return nil, err
	}
	if info.CurrentGame != 0 && info.CurrentGame != appID {
		return nil, gamestream.ErrBusy
	}
	// One stream per person: starting another ends the last one, as with any game here. Only now
	// that this one can go ahead, as a refusal shouldn't cost anyone what they were playing.
	for _, st := range mine {
		st.Session.Close()
	}
	var launched *gamestream.Launched
	if info.CurrentGame == 0 {
		launched, err = client.Launch(ctx, o)
	} else {
		launched, err = client.Resume(ctx, o)
	}
	if err != nil {
		return nil, err
	}
	rtsp, err := cleanSessionURL(launched.SessionURL, ip)
	if err != nil {
		return nil, err
	}
	sidb := make([]byte, 16)
	if _, err := rand.Read(sidb); err != nil {
		return nil, err
	}
	st := &stream{ID: hex.EncodeToString(sidb), User: user, RigID: rigID, AppID: appID, Title: title, Client: client}
	addr, err := netip.ParseAddr(ip)
	remote := err == nil && gamestream.Classify(addr) == gamestream.Public
	packet := 1392
	if remote {
		packet = 1024
	}
	opts := relay.Options{
		Program: program, Prepare: a.prepareFor(program, args), Log: a.Log,
		Start: relay.Start{Address: ip, AppVersion: info.Version, GfeVersion: info.GfeVersion, RTSP: rtsp, CodecModes: info.CodecModes,
			Width: q.Width, Height: q.Height, FPS: q.FPS, BitrateKbps: q.Bitrate, PacketSize: packet, Remote: remote, Key: o.Key, IV: o.IV},
		OnEnd: func(reason string) {
			a.mu.Lock()
			delete(a.streams, st.ID)
			a.mu.Unlock()
			a.Log.Info("rigs: a stream ended", "user", user)
		},
	}
	if v := strings.TrimSpace(set.PublicIP); v != "" {
		opts.PublicIP = []string{v}
	}
	if v := strings.TrimSpace(set.STUN); v != "" {
		opts.STUN = strings.Fields(strings.ReplaceAll(v, ",", " "))
	}
	if set.UDPPort > 1024 {
		opts.UDPPort = set.UDPPort
	}
	sess, err := relay.New(ctx, opts)
	if err != nil {
		return nil, err
	}
	st.Session = sess
	a.mu.Lock()
	a.streams[st.ID] = st
	a.mu.Unlock()
	select {
	case <-sess.Ended(): // before it was listed, so nothing else would take it off
		a.mu.Lock()
		delete(a.streams, st.ID)
		a.mu.Unlock()
		return nil, errors.New("the stream ended as it started")
	default:
	}
	a.Log.Info("rigs: a stream started", "user", user, "encrypted", true)
	return st, nil
}

// writeStartError says why a stream didn't start.
func writeStartError(w http.ResponseWriter, admin bool, err error) {
	var se *gamestream.StatusError
	switch {
	case errors.Is(err, relay.ErrNoProgram):
		writeJSON(w, 503, map[string]any{"error": "This server doesn't have its Moonlight program yet. It's a separate download: see Settings, Modules (WittyCouch Host).", "needProgram": true})
	case errors.Is(err, errFull):
		writeErr(w, 503, err.Error())
	case errors.Is(err, errStarting):
		writeErr(w, 409, err.Error())
	case errors.Is(err, gamestream.ErrBusy):
		writeErr(w, 409, "Another game is running on this PC, so this one can't start until it's closed.")
	case errors.Is(err, gamestream.ErrNotPaired):
		writeJSON(w, 409, map[string]any{"error": "This PC no longer trusts this server. An administrator has to pair it again.", "needPair": true})
	case errors.Is(err, gamestream.ErrCertChanged):
		writeErr(w, 409, "Something other than the PC that was paired answered, so it wasn't trusted.")
	case errors.As(err, &se):
		writeErr(w, 502, "The PC couldn't start it. "+se.Message)
	default:
		msg := "This PC couldn't be reached. It may be asleep or off, or Sunshine may not be running."
		if admin {
			msg = statusFor(true, nil, err).Message
		}
		writeErr(w, 502, msg)
	}
}

type sessionView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	FPS    int    `json:"fps,omitempty"`
}

func viewOfStream(st *stream) sessionView {
	info := st.Session.Info()
	return sessionView{ID: st.ID, Title: st.Title, Width: info.Width, Height: info.Height, FPS: info.FPS}
}

// POST /games/{id}/stream {width, height, fps, bitrate}: play a rig's game here.
func (a *App) handleRigStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, rig, appID := a.rigGameFor(w, r)
	if it == nil {
		return
	}
	var q playRequest
	if err := readJSON(r, &q); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	q.clean()
	who := service.Who(r)
	client, err := a.rigClient(rig)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	st := a.rigStatusNow(ctx, rig)
	if !a.rigMayJoin(rig, st, appID, who.User, who.Admin) {
		writeErr(w, 409, "Something else is running on this PC, so this can't start until it's closed.")
		return
	}
	s, err := a.startStream(ctx, who.User, client, appID, it.Title, q, rig.ID)
	if err != nil {
		writeStartError(w, who.Admin, err)
		return
	}
	a.mu.Lock()
	a.starter[rig.ID] = rigStarter{user: who.User, app: appID}
	delete(a.statuses, rig.ID)
	a.mu.Unlock()
	writeJSON(w, 200, viewOfStream(s))
}

// streamFor finds a stream of this person's.
func (a *App) streamFor(w http.ResponseWriter, r *http.Request) *stream {
	a.mu.Lock()
	st := a.streams[r.PathValue("sid")]
	a.mu.Unlock()
	if st == nil || st.User != service.Who(r).User {
		writeErr(w, 404, "That stream has ended.")
		return nil
	}
	return st
}

// GET /sessions/{sid}
func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	if st := a.streamFor(w, r); st != nil {
		writeJSON(w, 200, viewOfStream(st))
	}
}

// POST /sessions/{sid}/webrtc {sdp}
func (a *App) handleWebRTC(w http.ResponseWriter, r *http.Request) {
	st := a.streamFor(w, r)
	if st == nil {
		return
	}
	var b struct {
		SDP string `json:"sdp"`
	}
	if err := readJSON(r, &b); err != nil || len(b.SDP) > 256<<10 {
		writeErr(w, 400, "That offer couldn't be read.")
		return
	}
	answer, err := st.Session.Offer(r.Context(), b.SDP)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"sdp": answer})
}

// DELETE /sessions/{sid}?quit=1: stop streaming, and with quit, end the game too.
func (a *App) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	st := a.streamFor(w, r)
	if st == nil {
		return
	}
	st.Session.Close()
	if r.URL.Query().Get("quit") == "1" {
		if err := st.Client.Quit(r.Context()); err != nil {
			a.Log.Debug("rigs: ending the game", "err", err)
		}
		a.forgetRigStatus(st.RigID)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// endStreamsOf ends an account's streams (the account went).
func (a *App) endStreamsOf(user int64) {
	a.mu.Lock()
	var list []*stream
	for _, st := range a.streams {
		if st.User == user {
			list = append(list, st)
		}
	}
	a.mu.Unlock()
	for _, st := range list {
		st.Session.Close()
	}
}

// Busy says a stream is running.
func Busy() bool {
	appMu.Lock()
	a := shared
	appMu.Unlock()
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.streams) > 0
}
