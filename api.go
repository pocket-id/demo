package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

// httpClient bounds every outgoing request, so a stalled server can't block a template build or a visitor's request forever
var httpClient = &http.Client{Timeout: 30 * time.Second}

// apiError keeps the API's response so it can be relayed to the browser as is
type apiError struct {
	method, path string
	status       int
	body         []byte
}

func (e apiError) Error() string {
	return fmt.Sprintf("%s %s: %d: %s", e.method, e.path, e.status, e.body)
}

// call sends a JSON request to the instance's API as the static API key user and decodes the response into out
func (i instance) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	return i.send(ctx, method, path, reader, "application/json", out)
}

func (i instance) upload(ctx context.Context, method, path, filename, contentType string, content []byte) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return i.send(ctx, method, path, &body, w.FormDataContentType(), nil)
}

func (i instance) send(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, i.url+"/api"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", i.apiKey)
	req.Header.Set("Content-Type", contentType)

	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return apiError{method, path, res.StatusCode, body}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
