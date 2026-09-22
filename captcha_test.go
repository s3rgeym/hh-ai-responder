package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Отклик с капчей должен уйти повторно с тем же payload плюс captchaKey/Text/State,
// а после неверного ответа — запросить новую картинку.
func TestSendResponseSolvesCaptcha(t *testing.T) {
	var retries []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/captcha":
			if req.Method != http.MethodPost || req.URL.Query().Get("lang") != "RU" {
				t.Errorf("captcha key request = %s %s", req.Method, req.URL)
			}
			_, _ = io.WriteString(w, `{"key":"key-`+string(rune('0'+len(retries)))+`"}`)
		case "/captcha/picture":
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
		case "/applicant/vacancy_response/popup":
			_ = req.ParseForm()
			if req.PostForm.Get("captchaText") == "" {
				_, _ = io.WriteString(w, `{"hhcaptcha":{"isBot":true,"captchaState":"state-1"}}`)
				return
			}
			retries = append(retries, req.PostForm)
			if req.PostForm.Get("captchaText") != "верно" {
				_, _ = io.WriteString(w, `{"hhcaptcha":{"isBot":true,"captchaState":"state-2","captchaError":true}}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"success": "true"})
		default:
			t.Errorf("unexpected request %s", req.URL)
		}
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL)
	jar, err := NewMemoryPersistentJar(t.TempDir() + "/cookies.txt")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(base, []*http.Cookie{{Name: "_xsrf", Value: "tok", Path: "/"}})

	answers := make(chan string, 2)
	answers <- "неверно"
	answers <- "верно"
	r := &HHAIResponder{
		ctx:            t.Context(),
		baseURL:        base,
		identity:       browserIdentityFor(defaultBrowserPlatform),
		jar:            jar,
		requester:      NewHHRequester(t.Context(), &http.Client{Timeout: 5 * time.Second}, 0, false),
		captchaAnswers: answers,
	}

	canAsk, show := canAskCaptcha, showCaptchaPicture
	canAskCaptcha = func() bool { return true }
	showCaptchaPicture = func(string) {}
	defer func() { canAskCaptcha, showCaptchaPicture = canAsk, show }()

	payload := url.Values{"vacancy_id": {"42"}, "letter": {"Здравствуйте!"}}
	result, err := r.SendResponse(payload, server.URL+"/vacancy/42")
	if err != nil {
		t.Fatalf("SendResponse: %v", err)
	}
	if result["success"] != "true" {
		t.Fatalf("result = %v, want success", result)
	}

	if len(retries) != 2 {
		t.Fatalf("retries = %d, want 2", len(retries))
	}
	want := []struct{ key, text, state string }{
		{"key-0", "неверно", "state-1"},
		{"key-1", "верно", "state-2"},
	}
	for i, w := range want {
		got := retries[i]
		if got.Get("captchaKey") != w.key || got.Get("captchaText") != w.text || got.Get("captchaState") != w.state {
			t.Errorf("retry %d captcha = %q/%q/%q, want %q/%q/%q", i,
				got.Get("captchaKey"), got.Get("captchaText"), got.Get("captchaState"), w.key, w.text, w.state)
		}
		if got.Get("vacancy_id") != "42" || got.Get("letter") != "Здравствуйте!" {
			t.Errorf("retry %d lost original payload: %v", i, got)
		}
	}
}

// Без терминала (Docker, nohup) капчу спросить некому: ответ с капчей возвращается как есть,
// и цикл откликов останавливается прежним errBotBlocked.
func TestSolveCaptchaWithoutTerminalKeepsChallenge(t *testing.T) {
	canAsk := canAskCaptcha
	canAskCaptcha = func() bool { return false }
	defer func() { canAskCaptcha = canAsk }()

	challenge := map[string]any{"hhcaptcha": map[string]any{"isBot": true, "captchaState": "s"}}
	result, err := (&HHAIResponder{ctx: t.Context()}).solveCaptcha(url.Values{}, "", challenge)
	if err != nil {
		t.Fatal(err)
	}
	if challenged, _ := botChallenge(result); !challenged {
		t.Errorf("result = %v, want the original challenge", result)
	}
}
