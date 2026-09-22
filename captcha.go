package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// captchaAttempts ограничивает число картинок подряд на один отклик, чтобы опечатки
// не превращались в бесконечный цикл.
const captchaAttempts = 3

// stdinLines читает ответы на капчу из терминала. Один общий reader на весь процесс:
// bufio буферизует stdin, и второй reader потерял бы уже прочитанные строки.
var stdinLines = func() <-chan string {
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	return lines
}

// canAskCaptcha — есть ли живой терминал, где человек может ввести ответ.
// В Docker без -it и под nohup stdin не терминал, и тогда остаётся прежнее поведение:
// остановить отклики и попросить пройти капчу в браузере.
var canAskCaptcha = func() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// solveCaptcha повторяет отклик с ответом на капчу hh.ru так же, как это делает фронт:
// POST /captcha даёт ключ, по ключу отдаётся картинка, а текст с картинки уходит
// в тот же запрос отклика полями captchaKey, captchaText и captchaState.
func (r *HHAIResponder) solveCaptcha(payload url.Values, refererURL string, result map[string]any) (map[string]any, error) {
	if !canAskCaptcha() {
		return result, nil
	}
	if r.captchaAnswers == nil {
		r.captchaAnswers = stdinLines()
	}

	for attempt := 1; attempt <= captchaAttempts; attempt++ {
		challenged, state := botChallenge(result)
		if !challenged {
			return result, nil
		}

		key, err := r.captchaKey(refererURL)
		if err != nil {
			return nil, fmt.Errorf("captcha key: %w", err)
		}
		path, err := r.saveCaptchaPicture(key, refererURL)
		if err != nil {
			return nil, fmt.Errorf("captcha picture: %w", err)
		}
		showCaptchaPicture(path)

		if attempt > 1 {
			logger.Warn("Captcha answer was wrong, here is a new one")
		}
		// \a — звуковой сигнал терминала, чтобы капчу заметили, даже если окно свёрнуто
		fmt.Fprintf(os.Stderr, "\a\nhh.ru просит капчу. Картинка открыта: %s\nВведи текст с картинки (пусто — остановить отклики): ", path)

		var answer string
		select {
		case line, ok := <-r.captchaAnswers:
			if !ok {
				return result, nil
			}
			answer = strings.TrimSpace(line)
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
		_ = os.Remove(path)
		if answer == "" {
			return result, nil
		}

		retry := cloneValues(payload)
		retry.Set("captchaKey", key)
		retry.Set("captchaText", answer)
		if state != "" {
			retry.Set("captchaState", state)
		}
		result, err = r.postVacancyResponse(retry, refererURL)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (r *HHAIResponder) captchaKey(refererURL string) (string, error) {
	headers := map[string]string{
		"Accept":           "application/json",
		"X-Requested-With": "XMLHttpRequest",
		"X-Xsrftoken":      r.XSRFToken(),
		"Referer":          refererURL,
	}
	req, err := r.buildRequest(http.MethodPost, "/captcha?lang=RU", nil, headers)
	if err != nil {
		return "", err
	}
	resp, err := r.requester.Do(req)
	if err != nil {
		return "", err
	}
	if resp.Status != http.StatusOK {
		return "", unexpectedHTTPStatus(resp.Status)
	}

	var body struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return "", fmt.Errorf("non JSON response: %w", err)
	}
	if body.Key == "" {
		return "", errors.New("empty captcha key")
	}
	return body.Key, nil
}

func (r *HHAIResponder) saveCaptchaPicture(key, refererURL string) (string, error) {
	headers := map[string]string{
		"Accept":         "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
		"Sec-Fetch-Mode": "no-cors",
		"Sec-Fetch-Dest": "image",
		"Priority":       "i",
		"Referer":        refererURL,
	}
	req, err := r.buildRequest(http.MethodGet, "/captcha/picture?key="+url.QueryEscape(key), nil, headers)
	if err != nil {
		return "", err
	}
	// Картинку браузер грузит тегом <img>, а не переходом по ссылке
	req.Header.Del("Sec-Fetch-User")
	req.Header.Del("Upgrade-Insecure-Requests")

	resp, err := r.requester.Do(req)
	if err != nil {
		return "", err
	}
	if resp.Status != http.StatusOK {
		return "", unexpectedHTTPStatus(resp.Status)
	}

	ext := ".png"
	if strings.Contains(http.DetectContentType(resp.Body), "jpeg") {
		ext = ".jpg"
	}
	path := filepath.Join(os.TempDir(), "hh-captcha"+ext)
	if err := os.WriteFile(path, resp.Body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// showCaptchaPicture открывает картинку в системном просмотрщике; если не вышло,
// путь всё равно напечатан в приглашении.
var showCaptchaPicture = func(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		logger.Debug("Can't open captcha picture: %v", err)
		return
	}
	go func() { _ = cmd.Wait() }()
}
