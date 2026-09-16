package crmactions

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func init() {
	register(callWebhook{})
	register(sendMessage{})
	register(sendTemplate{})
}

// WebhookMaxTimeout caps how long one action may hold a worker. A rule pointed
// at a slow endpoint must not stall every other rule behind it.
const WebhookMaxTimeout = 10 * time.Second

// WebhookMaxBody bounds what is read back, so a misconfigured endpoint
// streaming gigabytes cannot exhaust memory. The body is only logged.
const WebhookMaxBody = 64 * 1024

type callWebhook struct{}

func (callWebhook) Type() string { return TypeCallWebhook }

func (callWebhook) Validate(cfg Config) error {
	raw := cfg.Str("url")
	if raw == "" {
		return fmt.Errorf("crmactions: call_webhook needs a url")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("crmactions: %q is not an http(s) url", raw)
	}
	switch strings.ToUpper(cfg.Str("method")) {
	case "", "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Errorf("crmactions: %q is not a supported method", cfg.Str("method"))
	}
	if seconds := cfg.Integer("timeout_s"); seconds < 0 || time.Duration(seconds)*time.Second > WebhookMaxTimeout {
		return fmt.Errorf("crmactions: webhook timeout must be between 1 and %d seconds",
			int(WebhookMaxTimeout.Seconds()))
	}
	return nil
}

func (callWebhook) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	endpoint := render(cfg.Str("url"), rc.Vars)
	method := strings.ToUpper(cfg.Str("method"))
	if method == "" {
		method = http.MethodPost
	}
	body := render(cfg.Str("body"), rc.Vars)

	if rc.DryRun {
		return map[string]any{"url": endpoint, "method": method, "body": body}, nil
	}
	if d.HTTPClient == nil {
		// Without the SSRF-safe client this action would happily fetch the
		// cloud metadata endpoint on behalf of whoever wrote the rule.
		return nil, Permanent{ErrUnavailable}
	}

	timeout := WebhookMaxTimeout
	if seconds := cfg.Integer("timeout_s"); seconds > 0 {
		timeout = time.Duration(seconds) * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, method, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, Permanent{err}
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range cfg.StringMap("headers") {
		req.Header.Set(key, render(value, rc.Vars))
	}
	if secret := cfg.Str("signing_secret"); cfg.Boolean("sign") && secret != "" {
		req.Header.Set("X-WACRM-Signature", sign(secret, body))
	}

	response, err := d.HTTPClient.Do(req)
	if err != nil {
		// A timeout or a refused connection may well work in a minute.
		return nil, Retryable{err}
	}
	defer response.Body.Close()

	preview, _ := io.ReadAll(io.LimitReader(response.Body, WebhookMaxBody))
	out := map[string]any{"status_code": response.StatusCode, "response": string(preview)}

	switch {
	case response.StatusCode >= 500:
		return out, Retryable{fmt.Errorf("crmactions: webhook returned %d", response.StatusCode)}
	case response.StatusCode >= 400:
		// The receiver is telling us the request is wrong. Sending it again
		// unchanged will be wrong again.
		return out, Permanent{fmt.Errorf("crmactions: webhook returned %d", response.StatusCode)}
	}
	return out, nil
}

// sign produces the HMAC the receiver checks, in the same form as the
// product's outbound webhooks.
func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// --- Messaging ---

type sendMessage struct{}

func (sendMessage) Type() string { return TypeSendMessage }

func (sendMessage) Validate(cfg Config) error { return requireText(cfg, "text") }

func (sendMessage) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	text := render(cfg.Str("text"), rc.Vars)
	if rc.DryRun {
		return map[string]any{"text": text}, nil
	}
	if d.Messenger == nil {
		return nil, Permanent{ErrUnavailable}
	}

	messageID, err := d.Messenger.SendText(ctx, rc.OrgID, rc.ContactID, text)
	if err != nil {
		// Outside the 24-hour window is the common failure and it is
		// permanent: the window does not reopen because we asked twice.
		return nil, Permanent{err}
	}
	return map[string]any{"message_id": messageID.String(), "text": text}, nil
}

type sendTemplate struct{}

func (sendTemplate) Type() string { return TypeSendTemplate }

func (sendTemplate) Validate(cfg Config) error {
	if _, ok := cfg.UUID("template_id"); !ok {
		return fmt.Errorf("crmactions: send_template needs a template_id")
	}
	return nil
}

func (sendTemplate) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	templateID, _ := cfg.UUID("template_id")

	params := map[string]any{}
	for key, value := range cfg.Object("param_mappings") {
		if text, isText := value.(string); isText {
			params[key] = render(text, rc.Vars)
			continue
		}
		params[key] = value
	}

	if rc.DryRun {
		return map[string]any{"template_id": templateID.String(), "params": params}, nil
	}
	if d.Messenger == nil {
		return nil, Permanent{ErrUnavailable}
	}

	messageID, err := d.Messenger.SendTemplate(ctx, rc.OrgID, rc.ContactID, templateID, params)
	if err != nil {
		return nil, Permanent{err}
	}
	return map[string]any{"message_id": messageID.String(), "template_id": templateID.String()}, nil
}
