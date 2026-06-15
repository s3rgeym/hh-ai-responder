// Этот код сгенериророван ChatGPT 5.5 из исходников на Python и частично
// переписан мною
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	userAgent            = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	acceptHeader         = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	acceptLanguageHeader = "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7"
	requestInterval      = 1200 * time.Millisecond
	defaultAIBaseURL     = "http://localhost:11434"
	defaultAIModel       = "llama3.1:8b"
	chatCompletionsPath  = "/v1/chat/completions"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

var (
	appCtx       = context.Background()
	currentLevel = LevelInfo
	logger       = NewColorLogger(os.Stderr)

	reTags       = regexp.MustCompile(`<[^>]*>`)
	reTokens     = regexp.MustCompile(`[[:alnum:]_-]+`)
	reResumeHash = regexp.MustCompile(`"latestResumeHash":"([a-f0-9]+)"`)
)

type Config struct {
	SearchURL    string
	CookiesPath  string
	LogLevel     string
	ResumeID     string
	MaxResponses int
	DryRun       bool
	AIBaseURL    string
	AIModel      string
	AIAPIKey     string
	AITimeout    time.Duration
	Contacts     string
}

type Vacancy struct {
	ID                     int               `json:"vacancyId"`
	Name                   string            `json:"name"`
	WorkSchedule           string            `json:"@workSchedule"`
	Links                  map[string]string `json:"links"`
	TotalResponsesCount    int               `json:"totalResponsesCount"`
	Area                   NamedObject       `json:"area"`
	Company                Company           `json:"company"`
	Compensation           Compensation      `json:"compensation"`
	CreationTime           string            `json:"creationTime"`
	LastChangeTime         ChangeTime        `json:"lastChangeTime"`
	UserLabels             []string          `json:"userLabels"`
	ResponseLetterRequired bool              `json:"@responseLetterRequired"`
	UserTestPresent        bool              `json:"userTestPresent"`
}

type NamedObject struct {
	Name string `json:"name"`
}

type Company struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	CompanySiteURL string `json:"companySiteUrl"`
}

type Compensation struct {
	From         int    `json:"from"`
	To           int    `json:"to"`
	CurrencyCode string `json:"currencyCode"`
}

type ChangeTime struct {
	Value string `json:"$"`
}

type VacancyTest struct {
	UIDPk     any    `json:"uidPk"`
	GUID      string `json:"guid"`
	StartTime any    `json:"startTime"`
	Required  any    `json:"required"`
	Tasks     []Task `json:"tasks"`
}

type Task struct {
	ID                 any        `json:"id"`
	Text               any        `json:"text"`
	Question           any        `json:"question"`
	Title              any        `json:"title"`
	Name               any        `json:"name"`
	Description        any        `json:"description"`
	CandidateSolutions []Solution `json:"candidateSolutions"`
}

type Solution struct {
	ID    any    `json:"id"`
	Text  string `json:"text"`
	Title string `json:"title"`
	Value string `json:"value"`
}

type VacancyEvent struct {
	Vacancy Vacancy
	Err     error
}

type HHAutoApplier struct {
	baseURL          *url.URL
	searchParams     url.Values
	cookiesPath      string
	maxResponses     int
	client           *http.Client
	jar              *MemoryPersistentJar
	resumeID         string
	latestResumeHash string
	resumes          map[string]string
	firstName        string
	middleName       string
	lastName         string
	email            string
	dryRun           bool
	ai               *AIClient
	contacts         string
	lastRequest      atomic.Int64
}

type AIClient struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionRequest struct {
	Model     string      `json:"model"`
	Messages  []AIMessage `json:"messages"`
	Stream    bool        `json:"stream"`
	MaxTokens int         `json:"max_tokens,omitempty"`
}

type ChatCompletionResponse struct {
	Choices []ChatCompletionChoice `json:"choices"`
}

type ChatCompletionChoice struct {
	Message AIMessage `json:"message"`
}

type AccountInfo struct {
	FirstName  string `json:"firstName"`
	MiddleName string `json:"middleName"`
	LastName   string `json:"lastName"`
	Email      string `json:"email"`
}

type ResumeTitle struct {
	String string `json:"string"`
}

type ResumeAttributes struct {
	Hash string `json:"hash"`
}

type ResumeItem struct {
	Title      []ResumeTitle    `json:"title"`
	Attributes ResumeAttributes `json:"_attributes"`
}

func NewHHAutoApplier(cfg Config) (*HHAutoApplier, error) {
	parsed, err := url.Parse(cfg.SearchURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid search URL: %s", cfg.SearchURL)
	}

	baseURL := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
	jar, err := NewMemoryPersistentJar(cfg.CookiesPath)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
	}

	applier := &HHAutoApplier{
		baseURL:      baseURL,
		searchParams: parsed.Query(),
		cookiesPath:  cfg.CookiesPath,
		maxResponses: cfg.MaxResponses,
		client:       client,
		jar:          jar,
		resumeID:     cfg.ResumeID,
		dryRun:       cfg.DryRun,
		ai:           NewAIClient(cfg.AIBaseURL, cfg.AIModel, cfg.AIAPIKey, cfg.AITimeout),
		contacts:     cfg.Contacts,
	}

	if err := applier.FetchProfileData(); err != nil {
		return nil, err
	}

	if applier.resumeID == "" {
		applier.resumeID = applier.latestResumeHash
	}

	return applier, nil
}

func (a *HHAutoApplier) Close() error {
	return a.SaveCookies()
}

func (a *HHAutoApplier) ResolveURL(endpoint string) string {
	ref, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	return a.baseURL.ResolveReference(ref).String()
}

func (a *HHAutoApplier) Request(method, endpoint string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(appCtx, method, a.ResolveURL(endpoint), body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", acceptLanguageHeader)
	req.Header.Set("Accept", acceptHeader)
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}

	if err := a.waitRequestSlot(); err != nil {
		return nil, err
	}

	resp, err := a.client.Do(req)
	if err != nil {
		logDebug("%d %s %s", resp.StatusCode, resp.Request.Method, resp.Request.URL.String())
	}
	return resp, err
}

func (a *HHAutoApplier) waitRequestSlot() error {
	for {
		last := a.lastRequest.Load()
		now := time.Now()

		if last > 0 {
			next := time.Unix(0, last).Add(requestInterval)
			wait := time.Until(next)
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
				case <-appCtx.Done():
					timer.Stop()
					return appCtx.Err()
				}
				now = time.Now()
			}
		}

		if a.lastRequest.CompareAndSwap(last, now.UnixNano()) {
			return nil
		}

		if err := appCtx.Err(); err != nil {
			return err
		}
	}
}

func (a *HHAutoApplier) GetCurrentResumeTitle() string {
	if title, ok := a.resumes[a.resumeID]; ok {
		return title
	}
	return ""
}

func (a *HHAutoApplier) GetFullName() string {
	return fmt.Sprintf("%s %s", a.firstName, a.lastName)
}

func (a *HHAutoApplier) XSRFToken() string {
	for _, cookie := range a.jar.Cookies(a.baseURL) {
		if cookie.Name == "_xsrf" {
			return cookie.Value
		}
	}
	return ""
}

func NewAIClient(baseURL, model, apiKey string, timeout time.Duration) *AIClient {
	return &AIClient{
		baseURL: normalizeBaseURL(baseURL),
		model:   model,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *AIClient) Chat(systemPrompt, userPrompt string, maxTokens int) (string, error) {
	payload := ChatCompletionRequest{
		Model:     c.model,
		Messages:  []AIMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}},
		Stream:    false,
		MaxTokens: maxTokens,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(appCtx, http.MethodPost, c.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	logDebug("%d %s %s", resp.StatusCode, resp.Request.Method, resp.Request.URL.String())

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := ensureStatusCode(resp.StatusCode, data); err != nil {
		return "", err
	}

	var result ChatCompletionResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", errors.New("ai response has no choices")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

func (c *AIClient) chatCompletionsURL() string {
	return joinURL(c.baseURL, chatCompletionsPath)
}

func normalizeBaseURL(baseURL string) string {
	if !strings.Contains(baseURL, "://") {
		return "http://" + baseURL
	}
	return baseURL
}

func (c *AIClient) GenerateLetter(v Vacancy, resumeTitle, fullName, contacts string) (string, error) {
	systemPrompt := strings.Join([]string{
		"Сгенерируй сопроводительное письмо от моего имени без использования markdown и списков, и не длинее 2048 символов. В котором опиши почему указанная вакансия подходит для моего резюме. Так же добавь в конец письма мои контакты, если они были указаны.",
	}, " ")

	userPrompt := fmt.Sprintf(
		"Название вакансии для отклика: %s\nКомпания, опубликовавшая вакансию: %s\nНазвание моего резюме: %s\nМое полное имя: %s\nМои контакты: %s\n",
		v.Name,
		v.Company.Name,
		resumeTitle,
		fullName,
		emptyDash(contacts),
	)

	return c.Chat(systemPrompt, userPrompt, 1024)
}

func (c *AIClient) ChooseSolutionID(task Task) (string, error) {
	if len(task.CandidateSolutions) == 0 {
		return "", errors.New("task has no candidate solutions")
	}

	var b strings.Builder
	b.WriteString("Вопрос:\n")
	b.WriteString(taskText(task))
	b.WriteString("\n\nВарианты ответа:\n")
	for _, solution := range task.CandidateSolutions {
		fmt.Fprintf(&b, "%s: %s\n", stringify(solution.ID), solutionLabel(solution))
	}
	b.WriteString("\nВыбери ID правильного или наиболее подходящего ответа. Пришли только ID.")

	answer, err := c.Chat(
		"Ты проходишь короткий тест работодателя. Выбери самый подходящий вариант. Ответь только ID варианта, без пояснений.",
		b.String(),
		32,
	)
	if err != nil {
		return "", err
	}

	selectedID, ok := parseSolutionID(answer, task.CandidateSolutions)
	if !ok {
		return "", fmt.Errorf("ai returned invalid solution id %q", answer)
	}

	return selectedID, nil
}

func (c *AIClient) AnswerFreeText(task Task) (string, error) {
	answer, err := c.Chat(
		"Ты проходишь короткий тест работодателя. Ответь кратко и профессионально, без Markdown. Если вопрос просит перейти по внешней ссылке, вежливо откажись переходить по сторонним ссылкам.",
		"Задание:\n"+taskText(task),
		256,
	)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(answer) == "" {
		return "", errors.New("ai returned empty text answer")
	}
	return strings.TrimSpace(answer), nil
}

func (a *HHAutoApplier) FetchProfileData() error {
	resp, err := a.Request(http.MethodGet, "/applicant/resumes", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := ensureStatus(resp); err != nil {
		return err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	target := []byte(`"applicantResumes":`)
	idx := bytes.Index(data, target)
	if idx == -1 {
		return errors.New("applicantResumes block not found on page")
	}

	jsonStart := data[idx+len(target):]

	var resumesList []ResumeItem
	decoder := json.NewDecoder(bytes.NewReader(jsonStart))
	if err := decoder.Decode(&resumesList); err != nil {
		return fmt.Errorf("failed to partially parse resumes: %w", err)
	}

	if len(resumesList) == 0 {
		return errors.New("no resumes found in applicantResumes list")
	}

	a.resumes = make(map[string]string)
	for _, item := range resumesList {
		hash := item.Attributes.Hash
		var title string
		if len(item.Title) > 0 {
			title = item.Title[0].String
		}
		if hash != "" {
			a.resumes[hash] = title
		}
	}

	a.resumeID = resumesList[0].Attributes.Hash

	targetAccount := []byte(`"account":`)
	idxAccount := bytes.Index(data, targetAccount)
	if idxAccount == -1 {
		return errors.New("account block not found on page")
	}

	jsonStartAccount := data[idxAccount+len(targetAccount):]

	var acc AccountInfo
	decoderAccount := json.NewDecoder(bytes.NewReader(jsonStartAccount))
	if err := decoderAccount.Decode(&acc); err != nil {
		return fmt.Errorf("failed to partially parse account: %w", err)
	}

	a.firstName = acc.FirstName
	a.middleName = acc.MiddleName
	a.lastName = acc.LastName
	a.email = acc.Email

	return nil
}
func (a *HHAutoApplier) GetVacancyTests(responseURL string) (map[string]VacancyTest, error) {
	resp, err := a.Request(http.MethodGet, responseURL, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := ensureStatus(resp); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tests map[string]VacancyTest
	if err := decodeEmbeddedJSON(data, `,"vacancyTests":`, &tests); err != nil {
		return nil, err
	}

	return tests, nil
}

func (a *HHAutoApplier) SendResponse(payload url.Values, refererURL string) (map[string]any, error) {
	token := a.XSRFToken()
	if token == "" {
		return nil, errors.New("xsrf token not found")
	}

	headers := map[string]string{
		"Content-Type":     "application/x-www-form-urlencoded",
		"X-Hhtmfrom":       "vacancy",
		"X-Hhtmsource":     "vacancy_response",
		"X-Requested-With": "XMLHttpRequest",
		"X-Xsrftoken":      token,
		"Referer":          refererURL,
	}

	encoded := payload.Encode()
	resp, err := a.Request(http.MethodPost, "/applicant/vacancy_response/popup", strings.NewReader(encoded), headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

func (a *HHAutoApplier) ApplyVacancy(vacancyID int, refererURL, letter string) (map[string]any, error) {
	token := a.XSRFToken()
	if token == "" {
		return nil, errors.New("xsrf token not found")
	}

	payload := url.Values{
		"_xsrf":            {token},
		"vacancy_id":       {strconv.Itoa(vacancyID)},
		"resume_hash":      {a.resumeID},
		"letter":           {letter},
		"ignore_postponed": {"true"},
	}

	return a.SendResponse(payload, refererURL)
}

func (a *HHAutoApplier) ApplyVacancyWithTest(vacancyID int, letter string) (map[string]any, error) {
	token := a.XSRFToken()
	if token == "" {
		return nil, errors.New("xsrf token not found")
	}

	responseURL := a.ResolveURL(fmt.Sprintf("/applicant/vacancy_response?vacancyId=%d&startedWithQuestion=false&hhtmFrom=vacancy", vacancyID))
	tests, err := a.GetVacancyTests(responseURL)
	if err != nil {
		return nil, err
	}

	test, ok := tests[strconv.Itoa(vacancyID)]
	if !ok {
		return nil, fmt.Errorf("vacancy test data not found for vacancy %d", vacancyID)
	}

	payload := url.Values{
		"_xsrf":            {token},
		"uidPk":            {stringify(test.UIDPk)},
		"guid":             {test.GUID},
		"startTime":        {stringify(test.StartTime)},
		"testRequired":     {stringify(test.Required)},
		"vacancy_id":       {strconv.Itoa(vacancyID)},
		"resume_hash":      {a.resumeID},
		"ignore_postponed": {"true"},
		"incomplete":       {"false"},
		"lux":              {"true"},
		"withoutTest":      {"no"},
		"letter":           {letter},
	}
	payload.Set("mark_applicant_visible_in_vacancy_country", "false")
	payload.Set("country_ids", "[]")

	for _, task := range test.Tasks {
		fieldName := "task_" + stringify(task.ID)
		if len(task.CandidateSolutions) == 0 {
			answer, err := a.ai.AnswerFreeText(task)
			if err != nil {
				return nil, fmt.Errorf("ai failed to answer text task %s: %w", stringify(task.ID), err)
			}
			payload.Set(fieldName+"_text", answer)
			continue
		}

		selectedID, err := a.ai.ChooseSolutionID(task)
		if err != nil {
			return nil, fmt.Errorf("ai failed to choose solution for task %s: %w", stringify(task.ID), err)
		}
		payload.Set(fieldName, selectedID)
	}

	return a.SendResponse(payload, responseURL)
}

func (a *HHAutoApplier) Vacancies() <-chan VacancyEvent {
	out := make(chan VacancyEvent)

	go func() {
		defer close(out)

		for page := 0; ; page++ {
			vacancies, err := a.fetchVacancyPage(page)
			if err != nil {
				sendVacancy(out, VacancyEvent{Err: err})
				return
			}

			logDebug("Найдено вакансий %d на странице %d", len(vacancies), page+1)
			if len(vacancies) == 0 {
				return
			}

			for _, vacancy := range vacancies {
				if !sendVacancy(out, VacancyEvent{Vacancy: vacancy}) {
					return
				}
			}
		}
	}()

	return out
}

func (a *HHAutoApplier) fetchVacancyPage(page int) ([]Vacancy, error) {
	params := cloneValues(a.searchParams)
	params.Set("page", strconv.Itoa(page))

	resp, err := a.Request(http.MethodGet, "/search/vacancy?"+params.Encode(), nil, nil)
	if err != nil {
		return nil, err
	}

	body, err := readAndClose(resp)
	if err != nil {
		return nil, err
	}
	if err := ensureStatusCode(resp.StatusCode, body); err != nil {
		return nil, err
	}

	var vacancies []Vacancy
	if err := decodeEmbeddedJSON(body, `,"vacancies":`, &vacancies); err != nil {
		return nil, err
	}

	return vacancies, nil
}

func sendVacancy(results chan<- VacancyEvent, result VacancyEvent) bool {
	select {
	case results <- result:
		return true
	case <-appCtx.Done():
		return false
	}
}

func (a *HHAutoApplier) ApplyVacancies() error {
	for item := range a.Vacancies() {
		if item.Err != nil {
			return item.Err
		}
		if err := appCtx.Err(); err != nil {
			break
		}

		vacancy := item.Vacancy
		vacancyURL := vacancy.Links["desktop"]

		if len(vacancy.UserLabels) > 0 {
			logDebug("Пропускаем вакансию с откликом: %s", vacancyURL)
			continue
		}

		if a.maxResponses > 0 && vacancy.TotalResponsesCount >= a.maxResponses {
			logDebug(
				"Пропускаем вакансию, так как количество откликов превысило порог игнорирования: %s; %d >= %d",
				vacancyURL,
				vacancy.TotalResponsesCount,
				a.maxResponses,
			)
			continue
		}

		letter, err := a.ai.GenerateLetter(vacancy, a.GetCurrentResumeTitle(), a.GetFullName(), a.contacts)
		if err != nil {
			logError("AI не смогла сгенерировать письмо: %s; %v", vacancyURL, err)
			continue
		}

		logDebug(
			"Пробуем откликнуться на вакансию %q (%s; откликов: %d): %.255s",
			vacancy.Name,
			vacancyURL,
			vacancy.TotalResponsesCount,
			letter,
		)

		if a.dryRun {
			continue
		}

		var responseResult map[string]any
		if vacancy.UserTestPresent {
			responseResult, err = a.ApplyVacancyWithTest(vacancy.ID, letter)
		} else {
			responseResult, err = a.ApplyVacancy(vacancy.ID, vacancyURL, letter)
		}
		if err != nil {
			logError("Ошибка при обработке ID %d: %v", vacancy.ID, err)
			continue
		}

		if errValue, ok := responseResult["error"].(string); ok && errValue != "" {
			if errValue == "negotiations-limit-exceeded" {
				logInfo("Суточный лимит откликов исчерпан")
				return nil
			}

			logError("%s: %s", errValue, vacancyURL)
			continue
		}

		if success, _ := responseResult["success"].(bool); success {
			logInfo("Отклик отправлен %s %s", vacancyURL, vacancy.Name)
			fmt.Println(vacancyURL)
			continue
		}

		logError("Неизвестная ошибка при отклике на вакансию: %s (%s)", vacancyURL, vacancy.Name)
	}

	if appCtx.Err() != nil {
		logWarn("Interrupted by user")
		return nil
	}

	logInfo("Завершили работу!")
	return nil
}

func (a *HHAutoApplier) SaveCookies() error {
	return a.jar.Save(a.cookiesPath)
}

type MemoryPersistentJar struct {
	mu      sync.Mutex
	cookies map[string][]*http.Cookie
}

func NewMemoryPersistentJar(cookiesPath string) (*MemoryPersistentJar, error) {
	jar := &MemoryPersistentJar{
		cookies: make(map[string][]*http.Cookie),
	}

	if _, err := os.Stat(cookiesPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cookie file not found: %s", cookiesPath)
		}
		return nil, err
	}

	file, err := os.Open(cookiesPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			parts = strings.Fields(line)
		}
		if len(parts) < 7 {
			continue
		}

		domain := parts[0]
		expiresUnix, _ := strconv.ParseInt(parts[4], 10, 64)
		cookie := &http.Cookie{
			Domain: domain,
			Path:   parts[2],
			Secure: strings.EqualFold(parts[3], "TRUE"),
			Name:   parts[5],
			Value:  parts[6],
		}
		if expiresUnix > 0 {
			cookie.Expires = time.Unix(expiresUnix, 0)
		}
		jar.cookies[domain] = append(jar.cookies[domain], cookie)
	}

	return jar, scanner.Err()
}

func (j *MemoryPersistentJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()

	host := u.Hostname()
	for _, cookie := range cookies {
		domain := cookie.Domain
		if domain == "" {
			domain = host
		}

		var updated []*http.Cookie
		exists := false
		for _, c := range j.cookies[domain] {
			if c.Name == cookie.Name && c.Path == cookie.Path {
				if cookie.Expires.IsZero() && !c.Expires.IsZero() {
					cookie.Expires = c.Expires
				}
				updated = append(updated, cookie)
				exists = true
			} else {
				updated = append(updated, c)
			}
		}
		if !exists {
			updated = append(updated, cookie)
		}
		j.cookies[domain] = updated
	}
}

func (j *MemoryPersistentJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()

	var matched []*http.Cookie
	host := u.Hostname()
	now := time.Now()

	for domain, list := range j.cookies {
		if domain == host || (strings.HasPrefix(domain, ".") && strings.HasSuffix(host, domain)) || strings.HasSuffix(host, "."+domain) {
			var active []*http.Cookie
			for _, cookie := range list {
				if !cookie.Expires.IsZero() && cookie.Expires.Before(now) {
					continue
				}
				if cookie.Secure && u.Scheme != "https" {
					continue
				}
				matched = append(matched, cookie)
				active = append(active, cookie)
			}
			j.cookies[domain] = active
		}
	}
	return matched
}

func (j *MemoryPersistentJar) Save(path string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	var buffer bytes.Buffer
	buffer.WriteString("# Netscape HTTP Cookie File\n")
	buffer.WriteString("# http://curl.haxx.se/rfc/cookie_spec.html\n")
	buffer.WriteString("# This is a generated file! Do not edit.\n\n")

	for domain, list := range j.cookies {
		for _, cookie := range list {
			if cookie.Name == "" {
				continue
			}
			expires := int64(0)
			if !cookie.Expires.IsZero() {
				expires = cookie.Expires.Unix()
			}
			secure := "FALSE"
			if cookie.Secure {
				secure = "TRUE"
			}
			cookiePath := cookie.Path
			if cookiePath == "" {
				cookiePath = "/"
			}
			row := []string{domain, "TRUE", cookiePath, secure, strconv.FormatInt(expires, 10), cookie.Name, cookie.Value}
			buffer.WriteString(strings.Join(row, "\t"))
			buffer.WriteByte('\n')
		}
	}

	return os.WriteFile(path, buffer.Bytes(), 0o600)
}

func decodeEmbeddedJSON[T any](data []byte, marker string, out *T) error {
	_, after, ok := bytes.Cut(data, []byte(marker))
	if !ok {
		return fmt.Errorf("marker %q not found in response", marker)
	}

	var raw json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(after))
	if err := decoder.Decode(&raw); err != nil {
		return err
	}

	return json.Unmarshal(raw, out)
}

func parseConfig() (Config, error) {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	cfg := Config{}

	flag.StringVar(&cfg.SearchURL, "u", envOrDefault("SEARCH_URL", ""), "URL поискового запроса")
	flag.StringVar(&cfg.CookiesPath, "c", filepath.Join(wd, "cookies.txt"), "Путь к cookies")
	flag.StringVar(&cfg.LogLevel, "l", "info", "Уровень логирования (debug, info, warn, error)")
	flag.StringVar(&cfg.ResumeID, "r", "", "ID резюме или будет использовано последнее")
	flag.IntVar(&cfg.MaxResponses, "mr", 0, "Пропускать вакансии, где количество откликов больше или равно N")
	flag.BoolVar(&cfg.DryRun, "d", false, "Не рассылать реальные отклики")
	flag.StringVar(&cfg.AIBaseURL, "ai-base-url", envFirst([]string{"AI_BASE_URL", "OPENAI_BASE_URL"}, defaultAIBaseURL), "Базовый URL OpenAI-compatible API")
	flag.StringVar(&cfg.AIModel, "ai-model", envFirst([]string{"AI_MODEL", "OPENAI_MODEL"}, defaultAIModel), "Модель AI")
	flag.StringVar(&cfg.AIAPIKey, "ai-api-key", envFirst([]string{"AI_API_KEY", "OPENAI_API_KEY"}, ""), "API key для OpenAI-compatible API")
	flag.DurationVar(&cfg.AITimeout, "ai-timeout", 90*time.Second, "Таймаут для запросов к AI")
	flag.StringVar(&cfg.Contacts, "C", envOrDefault("CONTACTS", ""), "Ваши контактные данные для связи")
	flag.Parse()

	if cfg.SearchURL == "" {
		return Config{}, errors.New("required flag: -u")
	}

	return cfg, nil
}

func setupLogging(cfg Config) {
	switch strings.ToLower(strings.TrimSpace(cfg.LogLevel)) {
	case "debug":
		currentLevel = LevelDebug
	case "warn", "warning":
		currentLevel = LevelWarn
	case "error":
		currentLevel = LevelError
	default:
		currentLevel = LevelInfo
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	appCtx = ctx

	// if err := loadDotEnv(".env"); err != nil {
	// 	fmt.Fprintf(os.Stderr, "failed to load .env: %v\n", err)
	// 	os.Exit(1)
	// }

	loadDotEnv(".env")

	cfg, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	setupLogging(cfg)

	applier, err := NewHHAutoApplier(cfg)
	if err != nil {
		logError("%v", err)
		os.Exit(1)
	}
	defer func() {
		if err := applier.Close(); err != nil {
			logError("Ошибка закрытия приложения: %v", err)
		}
	}()

	if err := applier.ApplyVacancies(); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

func cloneValues(values url.Values) url.Values {
	result := make(url.Values, len(values))
	for key, list := range values {
		result[key] = append([]string(nil), list...)
	}
	return result
}

func readAndClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func ensureStatus(resp *http.Response) error {
	body, err := readAndClose(resp)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return ensureStatusCode(resp.StatusCode, body)
}

func ensureStatusCode(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	if len(body) > 300 {
		body = body[:300]
	}
	return fmt.Errorf("unexpected HTTP status %d: %s", status, string(body))
}

func stringify(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprint(v)
	}
}

func taskText(task Task) string {
	parts := make([]string, 0, 5)
	for _, value := range []any{task.Description, task.Question, task.Text, task.Title, task.Name} {
		if text := cleanText(stringify(value)); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "Выберите наиболее подходящий вариант ответа."
	}
	return strings.Join(parts, "\n")
}

func solutionLabel(solution Solution) string {
	for _, text := range []string{solution.Text, solution.Title, solution.Value} {
		if cleaned := cleanText(text); cleaned != "" {
			return cleaned
		}
	}
	return stringify(solution.ID)
}

func parseSolutionID(answer string, solutions []Solution) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	for _, solution := range solutions {
		id := stringify(solution.ID)
		if answer == id {
			return id, true
		}
	}

	tokens := reTokens.FindAllString(answer, -1)
	for _, token := range tokens {
		for _, solution := range solutions {
			id := stringify(solution.ID)
			if token == id {
				return id, true
			}
		}
	}

	return "", false
}

func cleanText(s string) string {
	s = stripTags(s)
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func stripTags(s string) string {
	return reTags.ReplaceAllString(s, "")
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return strings.TrimSpace(s)
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envFirst(names []string, fallback string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return fallback
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = trimEnvValue(strings.TrimSpace(value))
		if key != "" {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}

func trimEnvValue(value string) string {
	if len(value) < 2 {
		return value
	}

	if value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
		value = strings.ReplaceAll(value, `\"`, `"`)
		value = strings.ReplaceAll(value, `\n`, "\n")
		return value
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}

	if idx := strings.Index(value, "#"); idx != -1 {
		value = strings.TrimSpace(value[:idx])
	}

	return value
}

func logDebug(format string, args ...any) {
	if currentLevel <= LevelDebug {
		writeLog("DEBUG", format, args...)
	}
}

func logInfo(format string, args ...any) {
	if currentLevel <= LevelInfo {
		writeLog("INFO", format, args...)
	}
}

func logWarn(format string, args ...any) {
	if currentLevel <= LevelWarn {
		writeLog("WARNING", format, args...)
	}
}

func logError(format string, args ...any) {
	if currentLevel <= LevelError {
		writeLog("ERROR", format, args...)
	}
}

func writeLog(level, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	logger.Write(level, message)
}

type ColorLogger struct {
	base *log.Logger
}

func NewColorLogger(output io.Writer) *ColorLogger {
	return &ColorLogger{
		base: log.New(output, "", log.LstdFlags),
	}
}

func (l *ColorLogger) Write(level, message string) {
	prefix := colorForLevel(level) + level + " - "
	l.base.Println(prefix + message + "\x1b[0m")
}

func colorForLevel(level string) string {
	switch level {
	case "DEBUG":
		return "\x1b[37;20m"
	case "INFO":
		return "\x1b[32;20m"
	case "WARNING":
		return "\x1b[33;20m"
	case "ERROR":
		return "\x1b[31;20m"
	default:
		return ""
	}
}

func joinURL(base string, paths ...string) string {
	base = strings.TrimRight(base, "/")
	if len(paths) == 0 {
		return base
	}

	var cleaned []string
	for _, p := range paths {
		p = strings.Trim(p, "/")
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}

	if len(cleaned) == 0 {
		return base
	}

	return base + "/" + strings.Join(cleaned, "/")
}
