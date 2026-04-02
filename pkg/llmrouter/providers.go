package llmrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// AnthropicProvider implements Anthropic Messages API.
type AnthropicProvider struct{}

func (p *AnthropicProvider) Complete(ctx context.Context, cfg ProviderConfig, req CompletionRequest) (CompletionResponse, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	// Build Anthropic messages
	var messages []map[string]string
	for _, m := range req.Messages {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}

	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": req.MaxTokens,
		"messages":   messages,
	}

	respBody, err := doJSON(ctx, baseURL+"/v1/messages", body, map[string]string{
		"x-api-key":         cfg.APIKeyRef,
		"anthropic-version": "2023-06-01",
	})
	if err != nil {
		return CompletionResponse{}, err
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("parse response: %w", err)
	}

	var text string
	for _, c := range result.Content {
		text += c.Text
	}

	return CompletionResponse{
		Content:   text,
		TokensIn:  result.Usage.InputTokens,
		TokensOut: result.Usage.OutputTokens,
	}, nil
}

// OpenAIProvider implements OpenAI Chat Completions API.
// Also works with any OpenAI-compatible endpoint (Azure, LiteLLM, vLLM).
type OpenAIProvider struct{}

func (p *OpenAIProvider) Complete(ctx context.Context, cfg ProviderConfig, req CompletionRequest) (CompletionResponse, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	var messages []map[string]string
	for _, m := range req.Messages {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}

	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": req.MaxTokens,
		"messages":   messages,
	}

	respBody, err := doJSON(ctx, baseURL+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer " + cfg.APIKeyRef,
	})
	if err != nil {
		return CompletionResponse{}, err
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("parse response: %w", err)
	}

	var text string
	if len(result.Choices) > 0 {
		text = result.Choices[0].Message.Content
	}

	return CompletionResponse{
		Content:   text,
		TokensIn:  result.Usage.PromptTokens,
		TokensOut: result.Usage.CompletionTokens,
	}, nil
}

// GeminiProvider implements Google Gemini GenerativeAI API.
type GeminiProvider struct{}

func (p *GeminiProvider) Complete(ctx context.Context, cfg ProviderConfig, req CompletionRequest) (CompletionResponse, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}

	var parts []map[string]string
	for _, m := range req.Messages {
		parts = append(parts, map[string]string{"text": m.Content})
	}

	body := map[string]any{
		"contents": []map[string]any{
			{"parts": parts},
		},
		"generationConfig": map[string]any{
			"maxOutputTokens": req.MaxTokens,
		},
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", baseURL, cfg.Model, cfg.APIKeyRef)
	respBody, err := doJSON(ctx, url, body, nil)
	if err != nil {
		return CompletionResponse{}, err
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("parse response: %w", err)
	}

	var text string
	if len(result.Candidates) > 0 {
		for _, p := range result.Candidates[0].Content.Parts {
			text += p.Text
		}
	}

	return CompletionResponse{
		Content:   text,
		TokensIn:  result.UsageMetadata.PromptTokenCount,
		TokensOut: result.UsageMetadata.CandidatesTokenCount,
	}, nil
}

// doJSON sends a JSON POST request and returns the response body.
func doJSON(ctx context.Context, url string, body any, headers map[string]string) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
