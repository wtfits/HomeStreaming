package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wtfits/homestreaming/internal/app"
	"github.com/wtfits/homestreaming/internal/config"
	"github.com/wtfits/homestreaming/internal/stream"
	"github.com/wtfits/homestreaming/internal/testutil"
)

type env struct {
	t     *testing.T
	app   *app.App
	srv   *httptest.Server
	root  string
	token string
}

func newEnv(t *testing.T, hwaccel string, idle time.Duration) *env {
	t.Helper()
	if !testutil.HaveFFmpeg() {
		t.Skip("ffmpeg not installed")
	}
	src, err := testutil.CachedTree()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "Movies-and-TV")
	if err := testutil.CopyTree(src, root); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	cfg, err := config.Load(config.Flags{DataDir: dataDir, HWAccel: hwaccel, LogLevel: "warn"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	cfg.Log.File = false
	cfg.HTTP.TrustedProxies = []string{"127.0.0.1"}
	opts := app.Options{Version: "test", Stderr: io.Discard, NoStartScan: true, NoWatcher: true, IdleTimeout: idle}
	a, err := app.New(context.Background(), cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go a.Sessions.Run(ctx)
	srv := httptest.NewServer(a.API.Handler())
	t.Cleanup(func() {
		cancel()
		srv.Close()
		a.Scanner.Wait()
		a.Close()
	})
	return &env{t: t, app: a, srv: srv, root: root}
}

type resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("json: %v: %s", err, r.Body)
	}
	return m
}

func (e *env) req(method, path string, body any, hdr map[string]string) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if e.token != "" {
		r.Header.Set("Authorization", "Bearer "+e.token)
	}
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{Status: res.StatusCode, Header: res.Header, Body: b}
}

func (e *env) must(method, path string, body any, want int) map[string]any {
	e.t.Helper()
	r := e.req(method, path, body, nil)
	if r.Status != want {
		e.t.Fatalf("%s %s: status %d want %d: %s", method, path, r.Status, want, r.Body)
	}
	if len(r.Body) == 0 {
		return nil
	}
	return r.json(e.t)
}

func (e *env) setupAndScan() {
	e.t.Helper()
	info := e.must("GET", "/api/v1/system/info", nil, 200)
	if info["setupRequired"] != true {
		e.t.Fatal("setup should be required")
	}
	out := e.must("POST", "/api/v1/system/setup", map[string]any{"username": "can", "password": "password123", "deviceId": "test-dev", "deviceName": "Go test"}, 201)
	e.token = out["token"].(string)
	lib := e.must("POST", "/api/v1/libraries", map[string]any{"name": "Torrent", "type": "mixed", "paths": []string{e.root}}, 201)
	e.waitTask(lib["scanTaskId"].(string))
}

func (e *env) waitTask(id string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		task := e.must("GET", "/api/v1/tasks/"+id, nil, 200)
		if task["status"] == "done" || task["status"] == "failed" {
			if task["status"] == "failed" {
				e.t.Fatalf("task failed: %v", task["error"])
			}
			return task
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatal("task timeout")
	return nil
}

func (e *env) findItem(typ, search string) map[string]any {
	e.t.Helper()
	out := e.must("GET", "/api/v1/items?type="+typ+"&search="+strings.ReplaceAll(search, " ", "%20"), nil, 200)
	items := out["items"].([]any)
	if len(items) != 1 {
		e.t.Fatalf("find %s %q: %d results", typ, search, len(items))
	}
	return items[0].(map[string]any)
}

func profile(platform string) stream.Profile {
	p := stream.BaseProfile(platform)
	p.DeviceID, p.DeviceName = "dev-"+platform, platform
	return p
}

func (e *env) playInfo(itemID string, body map[string]any) map[string]any {
	e.t.Helper()
	return e.must("POST", "/api/v1/items/"+itemID+"/play-info", body, 201)
}

func (e *env) ffmpegValidate(url string) {
	e.t.Helper()
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-i", url, "-f", "null", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("ffmpeg could not play %s: %v\n%s", url, err, out)
	}
}

func TestAPIEndToEnd(t *testing.T) {
	e := newEnv(t, "none", 30*time.Second)
	e.setupAndScan()

	// Setup is one-shot; security headers present.
	r := e.req("POST", "/api/v1/system/setup", map[string]any{"username": "x", "password": "password123"}, nil)
	if r.Status != 403 || !strings.Contains(string(r.Body), "setup_done") {
		t.Fatalf("second setup: %d %s", r.Status, r.Body)
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("X-Frame-Options") != "DENY" || r.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("headers: %v", r.Header)
	}

	// Library counts and series merge.
	libs := e.must("GET", "/api/v1/libraries", nil, 200)["items"].([]any)
	counts := libs[0].(map[string]any)["counts"].(map[string]any)
	if counts["movies"].(float64) != 4 || counts["series"].(float64) != 5 || counts["episodes"].(float64) != 8 {
		t.Fatalf("counts: %v", counts)
	}
	ad := e.findItem("series", "arrested")
	children := e.must("GET", "/api/v1/items/"+ad["id"].(string)+"/children", nil, 200)["items"].([]any)
	if len(children) != 2 || children[0].(map[string]any)["seasonNumber"].(float64) != 1 {
		t.Fatalf("AD seasons: %v", children)
	}

	// Item detail: preferred audio language beats the default flag.
	third := e.findItem("movie", "Third")
	detail := e.must("GET", "/api/v1/items/"+third["id"].(string), nil, 200)
	if len(detail["audioTracks"].([]any)) != 2 || detail["defaultAudioTrackId"].(float64) != 2 {
		t.Fatalf("third movie tracks: %v default %v", detail["audioTracks"], detail["defaultAudioTrackId"])
	}

	// Images: local poster with ETag/304; extracted thumb; no poster for Movie Name.
	r = e.req("GET", "/api/v1/items/"+third["id"].(string)+"/image/poster?w=200", nil, nil)
	if r.Status != 200 || r.Header.Get("Content-Type") != "image/jpeg" || r.Header.Get("ETag") == "" {
		t.Fatalf("poster: %d %v", r.Status, r.Header)
	}
	r2 := e.req("GET", "/api/v1/items/"+third["id"].(string)+"/image/poster?w=200", nil, map[string]string{"If-None-Match": r.Header.Get("ETag")})
	if r2.Status != 304 {
		t.Fatalf("etag: %d", r2.Status)
	}
	movie := e.findItem("movie", "Movie Name")
	mid := movie["id"].(string)
	if r := e.req("GET", "/api/v1/items/"+mid+"/image/poster", nil, nil); r.Status != 404 {
		t.Fatalf("movie name poster should 404: %d", r.Status)
	}
	if r := e.req("GET", "/api/v1/items/"+mid+"/image/thumb", nil, nil); r.Status != 200 {
		t.Fatalf("thumb: %d", r.Status)
	}

	// Direct file with Range; query token only on media routes.
	r = e.req("GET", "/api/v1/items/"+mid+"/file", nil, map[string]string{"Range": "bytes=100-199"})
	if r.Status != 206 || len(r.Body) != 100 || !strings.HasPrefix(r.Header.Get("Content-Range"), "bytes 100-199/") || r.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("range: %d %v", r.Status, r.Header)
	}
	tok := e.token
	e.token = ""
	if r := e.req("HEAD", "/api/v1/items/"+mid+"/file?token="+tok, nil, nil); r.Status != 200 {
		t.Fatalf("query token on media route: %d", r.Status)
	}
	if r := e.req("GET", "/api/v1/items?token="+tok, nil, nil); r.Status != 401 {
		t.Fatalf("query token must be rejected on non-media routes: %d", r.Status)
	}
	e.token = tok

	// Direct play decision.
	pi := e.playInfo(mid, map[string]any{"profile": profile("desktop")})
	if pi["method"] != "direct" || pi["engine"] != "html5" {
		t.Fatalf("direct: %v", pi)
	}

	// HEVC 10-bit HDR → transcode with tonemap (software), predictive playlist.
	hdr := e.findItem("movie", "Another Film")
	pi = e.playInfo(hdr["id"].(string), map[string]any{"profile": profile("desktop")})
	tc := pi["transcode"].(map[string]any)
	if pi["method"] != "transcode" || tc["backend"] != "software" || tc["video"].(map[string]any)["tonemap"] != true {
		t.Fatalf("hdr transcode: %v", pi)
	}
	sid := pi["playSessionId"].(string)
	idx := e.req("GET", "/api/v1/play/"+sid+"/index.m3u8", nil, nil)
	if strings.Count(string(idx.Body), "#EXTINF") != 4 || !strings.Contains(string(idx.Body), "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Fatalf("index: %s", idx.Body)
	}
	start := time.Now()
	seg := e.req("GET", "/api/v1/play/"+sid+"/seg00000.m4s", nil, nil)
	if seg.Status != 200 || time.Since(start) > 10*time.Second || seg.Header.Get("Content-Type") != "video/iso.segment" {
		t.Fatalf("seg0: %d after %v", seg.Status, time.Since(start))
	}
	// Tonemapped output is 8-bit BT.709.
	initR := e.req("GET", "/api/v1/play/"+sid+"/init.mp4", nil, nil)
	tmp := filepath.Join(t.TempDir(), "seg.mp4")
	os.WriteFile(tmp, append(initR.Body, seg.Body...), 0o644)
	probe, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=pix_fmt,color_transfer,codec_name", "-of", "default=nw=1", tmp).Output()
	if !strings.Contains(string(probe), "pix_fmt=yuv420p") || !strings.Contains(string(probe), "color_transfer=bt709") || !strings.Contains(string(probe), "codec_name=h264") {
		t.Fatalf("tonemap output: %s", probe)
	}
	e.ffmpegValidate(e.srv.URL + "/api/v1/play/" + sid + "/master.m3u8?token=" + e.token)
	e.must("DELETE", "/api/v1/play/"+sid, nil, 204)
	if _, err := os.Stat(filepath.Join(e.app.Cfg.Transcode.Dir, sid)); !os.IsNotExist(err) {
		t.Fatal("session dir not removed on DELETE")
	}

	// Seek outside the producing window restarts ffmpeg at 60 s.
	pi = e.playInfo(mid, map[string]any{"profile": profile("desktop"), "forceTranscode": true})
	if pi["method"] != "transcode" || pi["reasons"].([]any)[0] != "forced" {
		t.Fatalf("forced: %v", pi)
	}
	sid = pi["playSessionId"].(string)
	if r := e.req("GET", "/api/v1/play/"+sid+"/seg00010.m4s", nil, nil); r.Status != 200 {
		t.Fatalf("seg10: %d %s", r.Status, r.Body)
	}
	adm := e.must("GET", "/api/v1/admin/play-sessions/"+sid, nil, 200)
	args := strings.Join(toStrings(adm["ffmpegArgs"]), " ")
	if adm["restarts"].(float64) != 1 || !strings.Contains(args, "-ss 60 ") || !strings.Contains(args, "-output_ts_offset 60") || !strings.Contains(args, "-start_number 10") {
		t.Fatalf("restart: restarts=%v args=%s", adm["restarts"], args)
	}
	if r := e.req("GET", "/api/v1/play/"+sid+"/seg00099.m4s", nil, nil); r.Status != 404 {
		t.Fatalf("segment beyond playlist: %d", r.Status)
	}
	e.must("DELETE", "/api/v1/play/"+sid, nil, 204)

	// Remux: AC-3 default track on desktop → HLS copy video, AAC audio.
	pi = e.playInfo(third["id"].(string), map[string]any{"profile": profile("desktop"), "audioTrackId": 1})
	if pi["method"] != "remux" || pi["transcode"].(map[string]any)["audio"].(map[string]any)["codec"] != "aac" {
		t.Fatalf("remux: %v", pi)
	}
	sid = pi["playSessionId"].(string)
	var pl string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		pl = string(e.req("GET", "/api/v1/play/"+sid+"/index.m3u8", nil, nil).Body)
		if strings.Contains(pl, "#EXT-X-ENDLIST") {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !strings.Contains(pl, "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES") || !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Fatalf("remux playlist: %s", pl)
	}
	e.ffmpegValidate(e.srv.URL + "/api/v1/play/" + sid + "/master.m3u8?token=" + e.token)

	// Audio switch replaces the session.
	pi2 := e.playInfo(third["id"].(string), map[string]any{"profile": profile("desktop"), "audioTrackId": 2, "replacesPlaySessionId": sid, "startPositionMs": 10000})
	if _, ok := e.app.Sessions.Get(sid); ok {
		t.Fatal("replaced session still alive")
	}
	if pi2["method"] != "remux" || pi2["timelineOffsetMs"].(float64) != 10000 || pi2["reasons"].([]any)[0] != "audio_track_not_default" {
		t.Fatalf("switch: %v", pi2)
	}
	e.must("DELETE", "/api/v1/play/"+pi2["playSessionId"].(string), nil, 204)

	// Subtitles: embedded subrip, offset shift, external CP1251, Subs folder, native in-band.
	bcs := e.findItem("episode", "Saul")
	bd := e.must("GET", "/api/v1/items/"+bcs["id"].(string), nil, 200)
	if bd["defaultAudioTrackId"].(float64) != 3 {
		t.Fatalf("BCS default audio should be eng (3): %v", bd["defaultAudioTrackId"])
	}
	at := bd["audioTracks"].([]any)
	if at[1].(map[string]any)["language"] != "rus" {
		t.Fatalf("untagged LostFilm track should be rus: %v", at[1])
	}
	subID := bd["subtitleTracks"].([]any)[0].(map[string]any)["id"].(string)
	vtt := e.req("GET", "/api/v1/items/"+bcs["id"].(string)+"/subtitles/"+subID+".vtt", nil, nil)
	if vtt.Status != 200 || !strings.HasPrefix(string(vtt.Body), "WEBVTT") || !strings.Contains(string(vtt.Body), "Hello from the English subtitle") {
		t.Fatalf("embedded vtt: %d %s", vtt.Status, vtt.Body)
	}
	vtt = e.req("GET", "/api/v1/items/"+bcs["id"].(string)+"/subtitles/"+subID+".vtt?offsetMs=10000", nil, nil)
	if strings.Contains(string(vtt.Body), "Hello from") || !strings.Contains(string(vtt.Body), "00:00:02.0") {
		t.Fatalf("offset vtt: %s", vtt.Body)
	}
	e8 := e.findItem("episode", "Episode 8")
	ed := e.must("GET", "/api/v1/items/"+e8["id"].(string), nil, 200)
	var rusSub string
	for _, s := range ed["subtitleTracks"].([]any) {
		if s.(map[string]any)["language"] == "rus" {
			rusSub = s.(map[string]any)["id"].(string)
		}
	}
	vtt = e.req("GET", "/api/v1/items/"+e8["id"].(string)+"/subtitles/"+rusSub+".vtt", nil, nil)
	if !strings.Contains(string(vtt.Body), "Привет мир") {
		t.Fatalf("cp1251 subtitle: %s", vtt.Body)
	}
	pi = e.playInfo(bcs["id"].(string), map[string]any{"profile": profile("ios"), "subtitleTrackId": subID})
	if pi["method"] != "remux" || pi["subtitles"].(map[string]any)["mode"] != "inband" {
		t.Fatalf("ios inband: %v", pi)
	}
	master := string(e.req("GET", "/api/v1/play/"+pi["playSessionId"].(string)+"/master.m3u8", nil, nil).Body)
	if !strings.Contains(master, "TYPE=SUBTITLES") || !strings.Contains(master, `SUBTITLES="subs"`) {
		t.Fatalf("master with subs: %s", master)
	}
	e.must("DELETE", "/api/v1/play/"+pi["playSessionId"].(string), nil, 204)

	// Bitrate policy: remote clients above the cap transcode; LAN direct-plays.
	e.must("PATCH", "/api/v1/admin/settings", map[string]any{"remoteMaxBitrateKbps": 200}, 200)
	r = e.req("POST", "/api/v1/items/"+mid+"/play-info", map[string]any{"profile": profile("desktop")}, map[string]string{"X-Forwarded-For": "24.133.1.2"})
	pi = r.json(t)
	if pi["method"] != "transcode" || pi["reasons"].([]any)[0] != "bitrate" || pi["connection"].(map[string]any)["class"] != "remote" {
		t.Fatalf("remote cap: %v", pi)
	}
	e.must("DELETE", "/api/v1/play/"+pi["playSessionId"].(string), nil, 204)
	r = e.req("POST", "/api/v1/items/"+mid+"/play-info", map[string]any{"profile": profile("desktop")}, map[string]string{"X-Forwarded-For": "192.168.0.16"})
	if pi = r.json(t); pi["method"] != "direct" {
		t.Fatalf("lan: %v", pi)
	}
	e.must("PATCH", "/api/v1/admin/settings", map[string]any{"remoteMaxBitrateKbps": 6000}, 200)
	if r := e.req("PATCH", "/api/v1/admin/settings", map[string]any{"transcodeMaxSessions": 99}, nil); r.Status != 400 {
		t.Fatalf("settings validation: %d", r.Status)
	}

	// Progress → continue watching → played → next up.
	eps := e.must("GET", "/api/v1/items?type=episode&seriesId="+ad["id"].(string)+"&sort=episode", nil, 200)["items"].([]any)
	first := eps[0].(map[string]any)
	pi = e.playInfo(first["id"].(string), map[string]any{"profile": profile("desktop")})
	sid = pi["playSessionId"].(string)
	e.must("POST", "/api/v1/play/"+sid+"/progress", map[string]any{"positionMs": 8000, "state": "paused"}, 200)
	home := e.must("GET", "/api/v1/home", nil, 200)
	cw := home["continueWatching"].([]any)
	if len(cw) != 1 || cw[0].(map[string]any)["userData"].(map[string]any)["positionMs"].(float64) != 8000 {
		t.Fatalf("continue watching: %v", cw)
	}
	out := e.must("POST", "/api/v1/play/"+sid+"/progress", map[string]any{"positionMs": 19500, "state": "stopped"}, 200)
	if out["played"] != true {
		t.Fatalf("played threshold: %v", out)
	}
	home = e.must("GET", "/api/v1/home", nil, 200)
	nu := home["nextUp"].([]any)
	if len(home["continueWatching"].([]any)) != 0 || len(nu) != 1 || nu[0].(map[string]any)["id"] != eps[1].(map[string]any)["id"] {
		t.Fatalf("next up: %v", nu)
	}
	if _, ok := e.app.Sessions.Get(sid); ok {
		t.Fatal("stopped session still alive")
	}

	// CSRF: cookie auth without JSON content type.
	cookieReq, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/auth/logout", strings.NewReader("x"))
	cookieReq.Header.Set("Content-Type", "text/plain")
	cookieReq.AddCookie(&http.Cookie{Name: "hs_session", Value: e.token})
	cr, _ := http.DefaultClient.Do(cookieReq)
	if cr.StatusCode != 403 {
		t.Fatalf("csrf: %d", cr.StatusCode)
	}
	cr.Body.Close()

	// Lockout after 5 wrong passwords, then the per-IP limiter.
	for i := 1; i <= 5; i++ {
		r := e.req("POST", "/api/v1/auth/login", map[string]any{"username": "can", "password": "wrong-password"}, map[string]string{"Authorization": ""})
		want := 401
		if i == 5 {
			want = 423
		}
		if r.Status != want {
			t.Fatalf("login attempt %d: %d %s", i, r.Status, r.Body)
		}
	}
	limited := false
	for i := 0; i < 12; i++ {
		r := e.req("POST", "/api/v1/auth/login", map[string]any{"username": "other", "password": "x"}, map[string]string{"Authorization": ""})
		if r.Status == 429 {
			limited = r.Header.Get("Retry-After") != ""
			break
		}
	}
	if !limited {
		t.Fatal("login rate limit not hit")
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func TestHardwareFallbackAndIdle(t *testing.T) {
	e := newEnv(t, "nvenc", 2*time.Second)
	e.setupAndScan()
	st := e.must("GET", "/api/v1/admin/status", nil, 200)
	if st["hwaccel"].(map[string]any)["active"] != "none" {
		t.Skip("NVENC actually works here; fallback not testable")
	}
	hdr := e.findItem("movie", "Another Film")
	pi := e.playInfo(hdr["id"].(string), map[string]any{"profile": profile("desktop")})
	if pi["transcode"].(map[string]any)["backend"] != "software" {
		t.Fatalf("expected software fallback: %v", pi["transcode"])
	}
	sid := pi["playSessionId"].(string)
	if r := e.req("GET", "/api/v1/play/"+sid+"/seg00000.m4s", nil, nil); r.Status != 200 {
		t.Fatalf("software segment: %d", r.Status)
	}
	// Idle sessions are reaped and their directory removed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.app.Sessions.Get(sid); !ok {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, ok := e.app.Sessions.Get(sid); ok {
		t.Fatal("idle session not reaped")
	}
	if _, err := os.Stat(filepath.Join(e.app.Cfg.Transcode.Dir, sid)); !os.IsNotExist(err) {
		t.Fatal("idle session dir not removed")
	}
}
