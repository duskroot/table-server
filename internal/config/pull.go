package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type remoteSettings struct {
	BaseURL     string
	Namespace   string
	Environment string
	AppID       string
	File        string
	Token       string
}

func pull(ctx context.Context, settings remoteSettings) (json.RawMessage, int, error) {
	body, err := json.Marshal(map[string]any{
		"Namespace":   settings.Namespace,
		"Environment": settings.Environment,
		"AppId":       settings.AppID,
		"File":        settings.File,
		"Token":       settings.Token,
		"Fresh":       true,
	})
	if err != nil {
		return nil, 0, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(settings.BaseURL, "/")+"/cfg/v1/config/PullConfig", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("config center: %w", err)
	}
	defer resp.Body.Close()
	var payload struct {
		ResponseMetadata struct {
			RequestId string `json:"RequestId"`
		} `json:"ResponseMetadata"`
		Result struct {
			Content json.RawMessage `json:"Content"`
			Version int             `json:"Version"`
		} `json:"Result"`
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, 0, fmt.Errorf("config center: %w", err)
	}
	if payload.Error != nil {
		return nil, 0, fmt.Errorf("config center %s %s (%s)", payload.Error.Code, payload.Error.Message, payload.ResponseMetadata.RequestId)
	}
	if len(payload.Result.Content) == 0 {
		return nil, 0, fmt.Errorf("config center returned empty content (%s)", payload.ResponseMetadata.RequestId)
	}
	return payload.Result.Content, payload.Result.Version, nil
}
