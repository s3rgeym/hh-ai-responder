package main

import (
	"bytes"
	"context"
	"fmt"
	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"io"
	stdhttp "net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// hhTransport is the seam between HHRequester and whatever speaks HTTP to hh.ru:
// either net/http or a client that replays a real browser's TLS/HTTP2 handshake.
//
// The browser client reproduces the TLS ClientHello (JA3/JA4), the HTTP/2 SETTINGS
// frame, the pseudo-header order and the connection flow of the selected Chrome
// build. The order of the regular headers reaches the browser transport through
// the request context (see withHeaderOrder), because fhttp otherwise writes them
// alphabetically, which matches neither Chrome's navigation nor its fetch/XHR
// order.
type hhTransport interface {
	Do(req *stdhttp.Request) (*stdhttp.Response, error)
}

// headerOrderContextKey carries the desired wire order of request headers to the
// browser transport. It travels through the request context (not the header map)
// so the net/http fallback transport never sees it as a real header.
type headerOrderContextKey struct{}

func withHeaderOrder(ctx context.Context, order []string) context.Context {
	return context.WithValue(ctx, headerOrderContextKey{}, order)
}

func headerOrderFrom(ctx context.Context) []string {
	order, _ := ctx.Value(headerOrderContextKey{}).([]string)
	return order
}

// browserProfileOff disables fingerprint impersonation (plain net/http).
const browserProfileOff = "off"

// newHHTransport builds the transport used for every hh.ru request. profileName is
// a key of profiles.MappedTLSClients ("chrome_152", "firefox_132", ...) or "off".
func newHHTransport(jar *MemoryPersistentJar, profileName string, timeout time.Duration) (hhTransport, error) {
	if profileName == "" || strings.EqualFold(profileName, browserProfileOff) {
		return &stdhttp.Client{Jar: jar, Timeout: timeout}, nil
	}

	profile, ok := profiles.MappedTLSClients[strings.ToLower(profileName)]
	if !ok {
		known := make([]string, 0, len(profiles.MappedTLSClients))
		for name := range profiles.MappedTLSClients {
			known = append(known, name)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown browser profile %q, known profiles: %s",
			profileName, strings.Join(known, ", "))
	}

	client, err := tlsclient.NewHttpClient(
		tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(profile),
		tlsclient.WithCookieJar(browserJar{jar: jar}),
		tlsclient.WithTimeoutMilliseconds(int(timeout.Milliseconds())),
	)
	if err != nil {
		return nil, fmt.Errorf("create browser client: %w", err)
	}

	return &browserTransport{client: client}, nil
}

type browserTransport struct {
	client tlsclient.HttpClient
}

// hhTransportName describes the active transport for the startup log.
func hhTransportName(profileName string) string {
	if profileName == "" || strings.EqualFold(profileName, browserProfileOff) {
		return "net/http (отпечаток браузера не имитируется)"
	}
	return "имитация отпечатка браузера " + profileName
}

func (t *browserTransport) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	freq, err := mapRequestToBrowser(req)
	if err != nil {
		return nil, err
	}

	fresp, err := t.client.Do(freq)
	if err != nil {
		return nil, err
	}

	return mapResponseFromBrowser(fresp)
}

func mapRequestToBrowser(req *stdhttp.Request) (*fhttp.Request, error) {
	var body io.Reader
	if req.Body != nil {
		data, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		body = bytes.NewReader(data)
	}

	freq, err := fhttp.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create browser request: %w", err)
	}

	freq.Header = make(fhttp.Header, len(req.Header))
	for key, values := range req.Header {
		freq.Header[key] = append([]string(nil), values...)
	}

	if order := headerOrderFrom(req.Context()); len(order) > 0 {
		freq.Header[fhttp.HeaderOrderKey] = order
	}

	return freq, nil
}

func mapResponseFromBrowser(fresp *fhttp.Response) (*stdhttp.Response, error) {
	resp := &stdhttp.Response{
		Status:        fresp.Status,
		StatusCode:    fresp.StatusCode,
		Proto:         fresp.Proto,
		ProtoMajor:    fresp.ProtoMajor,
		ProtoMinor:    fresp.ProtoMinor,
		Header:        make(stdhttp.Header, len(fresp.Header)),
		Body:          fresp.Body,
		ContentLength: fresp.ContentLength,
		Close:         fresp.Close,
	}

	for key, values := range fresp.Header {
		resp.Header[key] = append([]string(nil), values...)
	}

	// Callers read resp.Request to learn the final URL after redirects; the browser
	// request type is foreign to net/http, so a stub carrying method and URL is built.
	if fresp.Request != nil && fresp.Request.URL != nil {
		if stub, err := stdhttp.NewRequestWithContext(reqContextOf(fresp), fresp.Request.Method, fresp.Request.URL.String(), nil); err == nil {
			resp.Request = stub
		}
	}

	return resp, nil
}

func reqContextOf(fresp *fhttp.Response) context.Context {
	if fresp.Request != nil && fresp.Request.Context() != nil {
		return fresp.Request.Context()
	}
	return context.Background()
}

// browserJar adapts the project's persistent cookie jar to the cookie jar
// interface of the browser client, so cookies keep being stored in the same
// Netscape-format file with the same value sanitizing as before.
type browserJar struct {
	jar *MemoryPersistentJar
}

func (j browserJar) Cookies(u *url.URL) []*fhttp.Cookie {
	in := j.jar.Cookies(u)
	out := make([]*fhttp.Cookie, 0, len(in))
	for _, c := range in {
		out = append(out, &fhttp.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Expires:  c.Expires,
			MaxAge:   c.MaxAge,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
			SameSite: fhttp.SameSite(c.SameSite),
		})
	}
	return out
}

func (j browserJar) SetCookies(u *url.URL, cookies []*fhttp.Cookie) {
	in := make([]*stdhttp.Cookie, 0, len(cookies))
	for _, c := range cookies {
		in = append(in, &stdhttp.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Expires:  c.Expires,
			MaxAge:   c.MaxAge,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
			SameSite: stdhttp.SameSite(c.SameSite),
		})
	}
	j.jar.SetCookies(u, in)
}
