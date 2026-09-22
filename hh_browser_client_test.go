package main

import (
	"bytes"
	"context"
	fhttp "github.com/bogdanfinn/fhttp"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	logger = NewLogger(io.Discard, LevelError)
	os.Exit(m.Run())
}

func newTestResponder(t *testing.T) *HHAIResponder {
	t.Helper()

	base := &url.URL{Scheme: "https", Host: "hh.ru"}
	return &HHAIResponder{ctx: t.Context(), baseURL: base, identity: browserIdentityFor(defaultBrowserPlatform)}
}

// A state-changing request without Origin is impossible for a browser, so it must be
// present on POSTs and absent on GETs (as Chrome does).
func TestBuildRequestSendsOriginOnlyForWrites(t *testing.T) {
	r := newTestResponder(t)

	post, err := r.buildRequest(http.MethodPost, "/applicant/vacancy_response/popup", strings.NewReader("a=1"), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if got := post.Header.Get("Origin"); got != "https://hh.ru" {
		t.Errorf("Origin = %q, want %q", got, "https://hh.ru")
	}
	if got := post.Header.Get("Priority"); got != "u=1, i" {
		t.Errorf("Priority = %q, want %q", got, "u=1, i")
	}
	if got := post.Header.Get("User-Agent"); !strings.Contains(got, "Chrome/") {
		t.Errorf("User-Agent = %q, want a Chrome UA", got)
	}

	get, err := r.buildRequest(http.MethodGet, "/vacancy/123", nil, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if got := get.Header.Get("Origin"); got != "" {
		t.Errorf("GET Origin = %q, want empty", got)
	}
}

// The platform in the headers must match the browser that created the hh.ru session:
// a Mac session suddenly browsing from Windows is what anti-bot scores.
func TestBrowserIdentityMatchesPlatform(t *testing.T) {
	cases := []struct {
		platform string
		wantUA   string
		wantCHUA string
	}{
		{"macos", "Macintosh; Intel Mac OS X 10_15_7", `"macOS"`},
		{"Windows", "Windows NT 10.0; Win64; x64", `"Windows"`},
	}

	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			r := newTestResponder(t)
			r.identity = browserIdentityFor(tc.platform)

			req, err := r.buildRequest(http.MethodGet, "/vacancy/123", nil, nil)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}

			if got := req.Header.Get("User-Agent"); !strings.Contains(got, tc.wantUA) {
				t.Errorf("User-Agent = %q, want it to contain %q", got, tc.wantUA)
			}
			if got := req.Header.Get("Sec-CH-UA-Platform"); got != tc.wantCHUA {
				t.Errorf("Sec-CH-UA-Platform = %q, want %q", got, tc.wantCHUA)
			}
			if got := req.Header.Get("Sec-CH-UA"); !strings.Contains(got, `"Google Chrome";v="`+chromeMajorVersion+`"`) {
				t.Errorf("Sec-CH-UA = %q, want it to name Chrome %s", got, chromeMajorVersion)
			}
		})
	}
}

func TestBuildRequestKeepsExplicitOrigin(t *testing.T) {
	r := newTestResponder(t)

	req, err := r.buildRequest(http.MethodPost, "/applicant/vacancy_response/popup", strings.NewReader("a=1"), map[string]string{
		"Origin": "https://hh.ru",
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if got := req.Header.Get("Origin"); got != "https://hh.ru" {
		t.Errorf("Origin = %q, want %q", got, "https://hh.ru")
	}
}

// Chrome never mixes the two profiles: a page load carries navigation metadata,
// an XHR carries cors/empty metadata. An HTML page fetched with XHR metadata is
// an easy tell for hh.ru anti-bot.
func TestBuildRequestSplitsDocumentAndXHRProfiles(t *testing.T) {
	r := newTestResponder(t)

	doc, err := r.buildRequest(http.MethodGet, "/vacancy/123", nil, nil)
	if err != nil {
		t.Fatalf("buildRequest document: %v", err)
	}

	docWant := map[string]string{
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
		"Priority":                  "u=0, i",
		"Accept-Encoding":           acceptEncodingHeader,
	}
	for key, want := range docWant {
		if got := doc.Header.Get(key); got != want {
			t.Errorf("document %s = %q, want %q", key, got, want)
		}
	}
	if got := doc.Header.Get("Accept"); !strings.Contains(got, "text/html") {
		t.Errorf("document Accept = %q, want a text/html accept", got)
	}
	if got := doc.Header.Get("X-Requested-With"); got != "" {
		t.Errorf("document X-Requested-With = %q, want empty", got)
	}
	if got := headerOrderFrom(doc.Context()); !slices.Equal(got, chromeDocumentHeaderOrder) {
		t.Errorf("document header order = %v, want chromeDocumentHeaderOrder", got)
	}

	xhr, err := r.buildRequest(http.MethodPost, "/applicant/vacancy_response/popup", strings.NewReader("a=1"), nil)
	if err != nil {
		t.Fatalf("buildRequest xhr: %v", err)
	}

	xhrWant := map[string]string{
		"Sec-Fetch-Mode":  "cors",
		"Sec-Fetch-Dest":  "empty",
		"Priority":        "u=1, i",
		"Accept":          "*/*",
		"Accept-Encoding": acceptEncodingHeader,
	}
	for key, want := range xhrWant {
		if got := xhr.Header.Get(key); got != want {
			t.Errorf("xhr %s = %q, want %q", key, got, want)
		}
	}
	if got := xhr.Header.Get("Sec-Fetch-User"); got != "" {
		t.Errorf("xhr Sec-Fetch-User = %q, want empty", got)
	}
	if got := xhr.Header.Get("Upgrade-Insecure-Requests"); got != "" {
		t.Errorf("xhr Upgrade-Insecure-Requests = %q, want empty", got)
	}
	if got := headerOrderFrom(xhr.Context()); !slices.Equal(got, chromeXHRHeaderOrder) {
		t.Errorf("xhr header order = %v, want chromeXHRHeaderOrder", got)
	}

	// A GET carrying X-Requested-With is an API call from an open page, not a
	// navigation (e.g. the chatik polling GETs).
	apiGet, err := r.buildRequest(http.MethodGet, "/chatik/api/chats", nil, map[string]string{
		"X-Requested-With": "XMLHttpRequest",
	})
	if err != nil {
		t.Fatalf("buildRequest api get: %v", err)
	}
	if got := apiGet.Header.Get("Sec-Fetch-Mode"); got != "cors" {
		t.Errorf("api get Sec-Fetch-Mode = %q, want cors", got)
	}
	if got := apiGet.Header.Get("Upgrade-Insecure-Requests"); got != "" {
		t.Errorf("api get Upgrade-Insecure-Requests = %q, want empty", got)
	}
}

func TestBotChallenge(t *testing.T) {
	cases := []struct {
		name      string
		result    map[string]any
		wantChall bool
		wantState string
	}{
		{
			name:   "success",
			result: map[string]any{"success": "true"},
		},
		{
			name:   "nil result",
			result: nil,
		},
		{
			name: "captcha with isBot",
			result: map[string]any{
				"hhcaptcha": map[string]any{"isBot": true, "captchaState": "zICw2REOBBWo"},
			},
			wantChall: true,
			wantState: "zICw2REOBBWo",
		},
		{
			name: "captcha state without isBot",
			result: map[string]any{
				"hhcaptcha": map[string]any{"isBot": false, "captchaState": "abc"},
			},
			wantChall: true,
			wantState: "abc",
		},
		{
			name:      "clean hhcaptcha object",
			result:    map[string]any{"hhcaptcha": map[string]any{"isBot": false}},
			wantChall: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			challenged, state := botChallenge(tc.result)
			if challenged != tc.wantChall {
				t.Errorf("challenged = %v, want %v", challenged, tc.wantChall)
			}
			if state != tc.wantState {
				t.Errorf("state = %q, want %q", state, tc.wantState)
			}
		})
	}
}

func TestNewHHTransportProfiles(t *testing.T) {
	jar, err := NewMemoryPersistentJar(t.TempDir() + "/cookies.txt")
	if err != nil {
		t.Fatalf("NewMemoryPersistentJar: %v", err)
	}

	off, err := newHHTransport(jar, browserProfileOff, time.Second)
	if err != nil {
		t.Fatalf("off transport: %v", err)
	}
	if _, ok := off.(*http.Client); !ok {
		t.Errorf("off transport = %T, want *http.Client", off)
	}

	browser, err := newHHTransport(jar, defaultBrowserProfile, time.Second)
	if err != nil {
		t.Fatalf("browser transport: %v", err)
	}
	if _, ok := browser.(*browserTransport); !ok {
		t.Errorf("browser transport = %T, want *browserTransport", browser)
	}

	if _, err := newHHTransport(jar, "nosuchbrowser_1", time.Second); err == nil {
		t.Error("unknown profile: want error, got nil")
	} else if !strings.Contains(err.Error(), "chrome_152") {
		t.Errorf("unknown profile error = %v, want it to list known profiles", err)
	}
}

func TestMapRequestToBrowserKeepsMethodURLHeadersAndBody(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://hh.ru/applicant/vacancy_response/popup", strings.NewReader("_xsrf=tok&letter=%D0%BF"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Xsrftoken", "tok")

	freq, err := mapRequestToBrowser(req)
	if err != nil {
		t.Fatalf("mapRequestToBrowser: %v", err)
	}

	if freq.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", freq.Method)
	}
	if freq.URL.String() != "https://hh.ru/applicant/vacancy_response/popup" {
		t.Errorf("url = %q", freq.URL.String())
	}
	if got := freq.Header.Get("X-Xsrftoken"); got != "tok" {
		t.Errorf("X-Xsrftoken = %q, want %q", got, "tok")
	}
	if freq.ContentLength != int64(len("_xsrf=tok&letter=%D0%BF")) {
		t.Errorf("ContentLength = %d", freq.ContentLength)
	}

	body, err := io.ReadAll(freq.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "_xsrf=tok&letter=%D0%BF" {
		t.Errorf("body = %q", body)
	}

	// Redirects replay the body through GetBody; without it a POST would be resent empty.
	replay, err := freq.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	again, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay: %v", err)
	}
	if !bytes.Equal(again, body) {
		t.Errorf("GetBody replayed %q, want %q", again, body)
	}
}

// The desired header wire order must reach fhttp through the request context;
// without it fhttp falls back to alphabetical order, which matches neither
// Chrome's navigation nor its fetch/XHR order.
func TestMapRequestToBrowserAppliesHeaderOrder(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://hh.ru/vacancy/123", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	freq, err := mapRequestToBrowser(req)
	if err != nil {
		t.Fatalf("mapRequestToBrowser: %v", err)
	}
	if _, ok := freq.Header[fhttp.HeaderOrderKey]; ok {
		t.Errorf("HeaderOrderKey set without context order: %v", freq.Header[fhttp.HeaderOrderKey])
	}

	ordered := req.WithContext(withHeaderOrder(req.Context(), chromeDocumentHeaderOrder))
	freq, err = mapRequestToBrowser(ordered)
	if err != nil {
		t.Fatalf("mapRequestToBrowser ordered: %v", err)
	}
	if got := freq.Header[fhttp.HeaderOrderKey]; !slices.Equal(got, chromeDocumentHeaderOrder) {
		t.Errorf("HeaderOrderKey = %v, want chromeDocumentHeaderOrder", got)
	}
}

func TestMapResponseFromBrowser(t *testing.T) {
	finalURL := &url.URL{Scheme: "https", Host: "hh.ru", Path: "/vacancy/123"}
	fresp := &fhttp.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/2.0",
		ProtoMajor:    2,
		Header:        fhttp.Header{"Set-Cookie": []string{"_xsrf=abc; Path=/; Secure"}},
		Body:          io.NopCloser(strings.NewReader(`{"success":"true"}`)),
		ContentLength: 17,
		Request:       &fhttp.Request{Method: http.MethodGet, URL: finalURL},
	}

	resp, err := mapResponseFromBrowser(fresp)
	if err != nil {
		t.Fatalf("mapResponseFromBrowser: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Set-Cookie"); got != "_xsrf=abc; Path=/; Secure" {
		t.Errorf("Set-Cookie = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"success":"true"}` {
		t.Errorf("body = %q", body)
	}
	if resp.Request == nil || resp.Request.URL.String() != finalURL.String() {
		t.Errorf("resp.Request = %+v, want final url %s", resp.Request, finalURL)
	}
}

func TestBrowserJarRoundTripKeepsXSRFReadable(t *testing.T) {
	cookiesPath := t.TempDir() + "/cookies.txt"
	jar, err := NewMemoryPersistentJar(cookiesPath)
	if err != nil {
		t.Fatalf("NewMemoryPersistentJar: %v", err)
	}

	r := &HHAIResponder{jar: jar, baseURL: &url.URL{Scheme: "https", Host: "hh.ru"}}
	bridge := browserJar{jar: jar}

	u, err := url.Parse("https://hh.ru/applicant/my_resumes")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	bridge.SetCookies(u, []*fhttp.Cookie{
		{Name: "_xsrf", Value: "tok123", Path: "/", Secure: true},
		{Name: "hhuid", Value: "42", Path: "/", Secure: true},
	})

	if got := r.XSRFToken(); got != "tok123" {
		t.Errorf("XSRFToken() = %q, want %q", got, "tok123")
	}

	sent := bridge.Cookies(u)
	if len(sent) != 2 {
		t.Fatalf("Cookies() returned %d cookies, want 2", len(sent))
	}
	if got := bridge.Cookies(u)[0]; got.Name != "_xsrf" || got.Value != "tok123" {
		t.Errorf("Cookies()[0] = %+v", got)
	}

	if err := jar.Save(cookiesPath); err != nil {
		t.Fatalf("save jar: %v", err)
	}

	saved, err := NewMemoryPersistentJar(cookiesPath)
	if err != nil {
		t.Fatalf("reload jar: %v", err)
	}
	r.jar = saved
	if got := r.XSRFToken(); got != "tok123" {
		t.Errorf("XSRFToken() after reload = %q, want %q", got, "tok123")
	}
}

func TestRequesterSpacing(t *testing.T) {
	r := NewHHRequester(context.Background(), &http.Client{}, 10*time.Second, false)
	if got := r.spacing(); got != 10*time.Second {
		t.Errorf("spacing without human pacing = %v, want 10s", got)
	}

	const (
		interval = 10 * time.Second
		floor    = 6 * time.Second   // 0.6 * interval
		ceil     = 18 * time.Second  // 1.8 * interval
		maxRest  = 150 * time.Second // longest added rest
		calls    = 200
	)

	r = NewHHRequester(context.Background(), &http.Client{}, interval, true)
	rests := 0

	for i := 0; i < calls; i++ {
		got := r.spacing()

		// The counter is reset to 0 exactly on the request that adds a long rest.
		restTaken := r.sinceBreak == 0

		if got < floor || got > ceil+maxRest {
			t.Fatalf("request %d: spacing %v outside [%v, %v]", i, got, floor, ceil+maxRest)
		}
		if !restTaken && got > ceil {
			t.Fatalf("request %d: spacing %v exceeds jitter ceiling %v without a rest", i, got, ceil)
		}
		if restTaken {
			rests++
		}
	}

	// breakEvery is drawn from 8..14, so 200 requests must include a few rests,
	// but not more than one per 8 requests.
	if rests == 0 || rests > calls/8 {
		t.Errorf("long rests = %d over %d requests, want between 1 and %d", rests, calls, calls/8)
	}
}
