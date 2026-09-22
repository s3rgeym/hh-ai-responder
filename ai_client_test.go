package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubChatServer records the last request body and answers like an OpenAI-compatible API.
func stubChatServer(t *testing.T, body *map[string]any) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != chatCompletionsPath {
			t.Errorf("path = %q, want %q", req.URL.Path, chatCompletionsPath)
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, body); err != nil {
			t.Errorf("request body is not JSON: %v (%s)", err, raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Здравствуйте."}}]}`)
	}))
}

// Reasoning-модели отвечают тем дольше, чем выше reasoning_effort, поэтому параметр
// должен управляться конфигом, а не быть зашитым в код.
func TestChatSendsConfiguredReasoningEffort(t *testing.T) {
	cases := []struct {
		name   string
		effort string
	}{
		{name: "medium", effort: "medium"},
		{name: "empty means provider default", effort: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			server := stubChatServer(t, &body)
			defer server.Close()

			client := NewAIClient(context.Background(), server.URL, "test-model", "", 5*time.Second, time.Second, 1, tc.effort)
			reply, err := client.Chat("system", "user", 16, 0.4)
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if reply != "Здравствуйте." {
				t.Errorf("reply = %q", reply)
			}

			got, present := body["reasoning_effort"]
			if tc.effort == "" {
				if present {
					t.Errorf("reasoning_effort = %v, want the parameter to be omitted", got)
				}
				return
			}
			if !present || got != tc.effort {
				t.Errorf("reasoning_effort = %v, want %q", got, tc.effort)
			}
		})
	}
}

func TestNewAIClientAppliesTimeouts(t *testing.T) {
	client := NewAIClient(context.Background(), "http://127.0.0.1:1", "m", "", 90*time.Second, 7*time.Second, 3, "medium")

	if client.client.Timeout != 90*time.Second {
		t.Errorf("client timeout = %v, want 90s", client.client.Timeout)
	}
	if client.attempts != 3 {
		t.Errorf("attempts = %d, want 3", client.attempts)
	}

	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.client.Transport)
	}
	if transport.TLSHandshakeTimeout != 7*time.Second {
		t.Errorf("TLS handshake timeout = %v, want 7s", transport.TLSHandshakeTimeout)
	}
}

// Стаж должен совпадать с тем, что показывает hh.ru: месяцы включительно,
// месяц смены работы не удваивается.
func TestExperienceMonthsMatchesHH(t *testing.T) {
	periods := [][2]string{
		{"2024-09-01", "2026-09-01"},
		{"2022-11-01", "2024-09-01"},
	}
	months := experienceMonths(periods, time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC))
	if got := formatExperience(months); got != "3 года 11 месяцев" {
		t.Errorf("formatExperience = %q, want %q", got, "3 года 11 месяцев")
	}

	current := [][2]string{{"2025-09-01", ""}}
	if got := experienceMonths(current, time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)); got != 13 {
		t.Errorf("open period = %d months, want 13", got)
	}
}

func TestFormatExperiencePlural(t *testing.T) {
	cases := map[int]string{
		1:   "1 месяц",
		12:  "1 год",
		25:  "2 года 1 месяц",
		60:  "5 лет",
		137: "11 лет 5 месяцев",
	}
	for months, want := range cases {
		if got := formatExperience(months); got != want {
			t.Errorf("formatExperience(%d) = %q, want %q", months, got, want)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	in := `<p><strong>Обязанности:</strong></p><ul><li>доработка 1С:ERP &amp; УТ</li><li>обмены</li></ul><p>Офис<br/>5/2</p>`
	want := "Обязанности:\n- доработка 1С:ERP & УТ\n- обмены\nОфис\n5/2"
	if got := htmlToText(in); got != want {
		t.Errorf("htmlToText =\n%q\nwant\n%q", got, want)
	}
}

func TestResumeTextAcceptsBothShapes(t *testing.T) {
	cases := map[string]string{
		`"Программист 1С &amp; ERP"`:                "Программист 1С & ERP",
		`[{"string":"Программист 1С"}]`:             "Программист 1С",
		`[{"string":"первая"},{"string":"вторая"}]`: "первая\nвторая",
		`null`: "",
		``:     "",
	}
	for raw, want := range cases {
		if got := resumeText(json.RawMessage(raw)); got != want {
			t.Errorf("resumeText(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestLetterLengthPenalty(t *testing.T) {
	cases := map[int]int{
		300: letterMinChars - 300,
		550: 0,
		700: 700 - letterMaxChars,
	}
	for n, want := range cases {
		if got := letterLengthPenalty(strings.Repeat("я", n)); got != want {
			t.Errorf("letterLengthPenalty(%d chars) = %d, want %d", n, got, want)
		}
	}
}
