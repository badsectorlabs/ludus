package ludusapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const pluginTokenRefreshInterval = 24 * time.Hour

type PocketBaseConnection struct {
	URL               string
	Token             string
	CertificateSHA256 string
}

type PocketBaseAPIError struct {
	Status  int
	Message string
	Data    map[string]any
}

func (e *PocketBaseAPIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("PocketBase API returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("PocketBase API returned HTTP %d: %s", e.Status, e.Message)
}

type PocketBaseClient struct {
	baseURL   *url.URL
	http      *http.Client
	tokenMu   sync.RWMutex
	token     string
	refreshMu sync.Mutex
	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	done      chan struct{}
}

func NewPocketBaseClient(connection PocketBaseConnection) (*PocketBaseClient, error) {
	baseURL, err := url.Parse(connection.URL)
	if err != nil {
		return nil, fmt.Errorf("parse PocketBase URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("PocketBase URL must use http or https")
	}
	if baseURL.Host == "" || !isLoopbackHost(baseURL.Hostname()) {
		return nil, fmt.Errorf("PocketBase URL must target the local host")
	}
	if connection.Token == "" {
		return nil, fmt.Errorf("PocketBase token is required")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if baseURL.Scheme == "https" {
		fingerprint, err := hex.DecodeString(connection.CertificateSHA256)
		if err != nil || len(fingerprint) != sha256.Size {
			return nil, fmt.Errorf("PocketBase HTTPS certificate SHA-256 fingerprint is invalid")
		}
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // Identity is checked against the exact host-provided certificate below.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("PocketBase TLS peer did not provide a certificate")
				}
				actual := sha256.Sum256(state.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(actual[:], fingerprint) != 1 {
					return errors.New("PocketBase TLS certificate fingerprint mismatch")
				}
				return nil
			},
		}
	}

	return &PocketBaseClient{
		baseURL: baseURL,
		http: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		token: connection.Token,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *PocketBaseClient) StartTokenRefresh(logger *slog.Logger) {
	c.startOnce.Do(func() {
		go func() {
			defer close(c.done)
			ticker := time.NewTicker(pluginTokenRefreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := c.refreshToken(context.Background(), true); err != nil && logger != nil {
						logger.Error("plugin PocketBase token refresh failed", "error", err)
					}
				case <-c.stop:
					return
				}
			}
		}()
	})
}

func (c *PocketBaseClient) Close() {
	c.stopOnce.Do(func() {
		close(c.stop)
		c.startOnce.Do(func() {
			close(c.done)
		})
		<-c.done
		c.http.CloseIdleConnections()
	})
}

func (c *PocketBaseClient) FindRecordByID(ctx context.Context, collection, id string, expand ...string) (*core.Record, error) {
	query := url.Values{}
	if len(expand) > 0 {
		query.Set("expand", strings.Join(expand, ","))
	}
	path := "/api/collections/" + url.PathEscape(collection) + "/records/" + url.PathEscape(id)
	var raw map[string]any
	if err := c.requestJSON(ctx, http.MethodGet, path, query, nil, &raw); err != nil {
		return nil, err
	}
	return pocketBaseRecord(collection, raw), nil
}

func (c *PocketBaseClient) FindFirstRecordByData(ctx context.Context, collection, field string, value any, expand ...string) (*core.Record, error) {
	if field == "" || strings.ContainsAny(field, " \t\r\n=()'\"") {
		return nil, fmt.Errorf("invalid PocketBase field name %q", field)
	}
	encodedValue, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode PocketBase filter value: %w", err)
	}
	records, err := c.ListRecords(ctx, collection, fmt.Sprintf("%s = %s", field, encodedValue), expand...)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("record not found in %s where %s = %v", collection, field, value)
	}
	return records[0], nil
}

func (c *PocketBaseClient) ListRecords(ctx context.Context, collection, filter string, expand ...string) ([]*core.Record, error) {
	const perPage = 500
	page := 1
	var records []*core.Record
	for {
		query := url.Values{
			"page":    []string{strconv.Itoa(page)},
			"perPage": []string{strconv.Itoa(perPage)},
		}
		if filter != "" {
			query.Set("filter", filter)
		}
		if len(expand) > 0 {
			query.Set("expand", strings.Join(expand, ","))
		}
		var response struct {
			Page       int              `json:"page"`
			TotalPages int              `json:"totalPages"`
			Items      []map[string]any `json:"items"`
		}
		path := "/api/collections/" + url.PathEscape(collection) + "/records"
		if err := c.requestJSON(ctx, http.MethodGet, path, query, nil, &response); err != nil {
			return nil, err
		}
		for _, raw := range response.Items {
			records = append(records, pocketBaseRecord(collection, raw))
		}
		if response.TotalPages <= page || len(response.Items) == 0 {
			return records, nil
		}
		page++
	}
}

func (c *PocketBaseClient) UpdateRecord(ctx context.Context, collection, id string, fields map[string]any) (*core.Record, error) {
	path := "/api/collections/" + url.PathEscape(collection) + "/records/" + url.PathEscape(id)
	var raw map[string]any
	if err := c.requestJSON(ctx, http.MethodPatch, path, nil, fields, &raw); err != nil {
		return nil, err
	}
	return pocketBaseRecord(collection, raw), nil
}

func pocketBaseRecord(collection string, raw map[string]any) *core.Record {
	if collection == "" {
		collection, _ = raw["collectionName"].(string)
	}
	record := core.NewRecord(core.NewBaseCollection(collection))
	expand, _ := raw["expand"].(map[string]any)
	delete(raw, "expand")
	record.Load(raw)
	if len(expand) > 0 {
		record.SetExpand(pocketBaseExpand(expand))
	}
	return record
}

func pocketBaseExpand(raw map[string]any) map[string]any {
	expand := make(map[string]any, len(raw))
	for field, value := range raw {
		switch typed := value.(type) {
		case map[string]any:
			expand[field] = pocketBaseRecord("", typed)
		case []any:
			records := make([]*core.Record, 0, len(typed))
			for _, item := range typed {
				if record, ok := item.(map[string]any); ok {
					records = append(records, pocketBaseRecord("", record))
				}
			}
			expand[field] = records
		}
	}
	return expand
}

func (c *PocketBaseClient) requestJSON(ctx context.Context, method, path string, query url.Values, body any, result any) error {
	if err := c.refreshToken(ctx, false); err != nil {
		return err
	}
	status, responseBody, err := c.do(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		if refreshErr := c.refreshToken(ctx, true); refreshErr != nil {
			return refreshErr
		}
		status, responseBody, err = c.do(ctx, method, path, query, body)
		if err != nil {
			return err
		}
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return decodePocketBaseAPIError(status, responseBody)
	}
	if result == nil || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, result); err != nil {
		return fmt.Errorf("decode PocketBase API response: %w", err)
	}
	return nil
}

func (c *PocketBaseClient) do(ctx context.Context, method, path string, query url.Values, body any) (int, []byte, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("encode PocketBase API request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return 0, nil, fmt.Errorf("create PocketBase API request: %w", err)
	}
	request.Header.Set("Authorization", c.currentToken())
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("call PocketBase API: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read PocketBase API response: %w", err)
	}
	return response.StatusCode, responseBody, nil
}

func decodePocketBaseAPIError(status int, body []byte) error {
	apiError := &PocketBaseAPIError{Status: status}
	var response struct {
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &response) == nil {
		apiError.Message = response.Message
		apiError.Data = response.Data
	}
	return apiError
}

func (c *PocketBaseClient) currentToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *PocketBaseClient) setToken(token string) {
	c.tokenMu.Lock()
	c.token = token
	c.tokenMu.Unlock()
}

func (c *PocketBaseClient) refreshToken(ctx context.Context, force bool) error {
	if !force && !tokenNeedsRefresh(c.currentToken(), 48*time.Hour) {
		return nil
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if !force && !tokenNeedsRefresh(c.currentToken(), 48*time.Hour) {
		return nil
	}
	var response struct {
		Token string `json:"token"`
	}
	status, responseBody, err := c.do(ctx, http.MethodPost, "/api/collections/_superusers/auth-refresh", nil, nil)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return decodePocketBaseAPIError(status, responseBody)
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return fmt.Errorf("decode PocketBase token refresh response: %w", err)
	}
	if response.Token == "" {
		return errors.New("PocketBase token refresh returned an empty token")
	}
	c.setToken(response.Token)
	return nil
}

func tokenNeedsRefresh(token string, within time.Duration) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.ExpiresAt == 0 {
		return false
	}
	return time.Until(time.Unix(claims.ExpiresAt, 0)) <= within
}
