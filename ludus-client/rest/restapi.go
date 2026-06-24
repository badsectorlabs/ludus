package rest

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ludus/logger"

	"github.com/briandowns/spinner"
	resty "github.com/go-resty/resty/v2"
)

type ErrorStruct struct {
	Error string `json:"error"`
}

const APIBasePath = "/api/v2"

// apiPrefix matches both APIBasePath and PocketBase's /api/collections/* —
// helpers below treat any "/api/" path as already-rooted and only prepend
// APIBasePath when callers pass a relative path.
const apiPrefix = "/api/"

var user string

func InitClient(url string, apiKey string, proxy string, verify bool, debug bool, versionString string) *resty.Client {
	var client = resty.New()
	if debug {
		client.SetDebug(true)
		logger.InitLogger(debug)
	}
	if len(apiKey) > 0 && strings.Contains(apiKey, ".") {
		user = apiKey[:strings.IndexByte(apiKey, '.')]
	} else {
		user = "[No API key loaded]"
	}

	client.SetHeader("User-Agent", fmt.Sprintf("ludus-client/%s ", versionString))

	if apiKey != "" {
		client.SetHeader("X-API-KEY", apiKey)
	} else {
		logger.Logger.Fatal("No API key provided to InitClient")
	}

	if url != "" {
		client.SetBaseURL(url)
		logger.Logger.Debug("Endpoint URL: ", url)
	}

	if proxy != "" {
		client.SetProxy(proxy)
		logger.Logger.Debug("Endpoint Proxy: ", proxy)
	}

	if !verify {
		client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
		logger.Logger.Debug("Endpoint SSL Verify: ", verify)
	}

	client.OnRequestLog(func(r *resty.RequestLog) error {
		// mask API key header
		var apiKeyMasked string
		apiKey = r.Header.Get("X-API-KEY")
		if len(apiKey) > 4 && strings.Contains(apiKey, ".") {
			parts := strings.Split(apiKey, ".")
			if len(parts) == 2 && len(parts[1]) >= 10 {
				secondPart := parts[1]
				apiKeyMasked = parts[0] + "." + secondPart[:3] + "***REDACTED***" + secondPart[len(secondPart)-3:]
			} else {
				apiKeyMasked = parts[0] + ".***Less than 10 characters?***"
			}
		} else {
			apiKeyMasked = "API Key not set"
		}
		r.Header.Set("X-API-KEY", apiKeyMasked)
		return nil
	})

	return client
}

func prettyPrintError(errorString string) {

	if errorString == "Client sent an HTTP request to an HTTPS server." {
		logger.Logger.Error("Your Ludus server is using HTTPS. Make sure your URL includes 'https://'")
		return
	}

	var parsedError ErrorStruct
	err := json.Unmarshal([]byte(errorString), &parsedError)
	if err != nil {
		logger.Logger.Fatalf("%s\nCheck the IP/hostname and port in the URL provided to ludus to ensure it is correct.", errorString)
		return
	}

	logger.Logger.Error(parsedError.Error)
}

type PocketBaseErrorStruct struct {
	Data    any    `json:"data"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func prettyPrintPocketBaseError(errorBytes []byte) error {
	var parsedError PocketBaseErrorStruct
	err := json.Unmarshal(errorBytes, &parsedError)
	if err != nil {
		return fmt.Errorf("failed to parse PocketBase error: %w", err)
	}
	if parsedError.Message == "Something went wrong while processing your request." {
		logger.Logger.Error("Check the PocketBase logs for crash details")
	} else if parsedError.Message == "" {
		return fmt.Errorf("not an error from PocketBase")
	} else {
		logger.Logger.Error(parsedError.Message)
	}

	return nil
}

// PBLookupRecordID resolves a PocketBase record's internal ID by querying the
// list endpoint with a filter on a unique user-facing field (e.g. blueprintID,
// rangeID). Returns the first matching record's id. The collection's ListRule
// must permit the caller for this to return a hit.
func PBLookupRecordID(client *resty.Client, collection, field, value string) (string, error) {
	filter := fmt.Sprintf(`%s = "%s"`, field, strings.ReplaceAll(value, `"`, `\"`))
	path := fmt.Sprintf("/api/collections/%s/records?perPage=1&filter=%s",
		collection,
		strings.ReplaceAll(filter, " ", "%20"))
	body, ok := GenericGet(client, path)
	if !ok {
		return "", fmt.Errorf("error looking up %s record by %s=%q", collection, field, value)
	}
	var listResp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return "", fmt.Errorf("decode %s lookup response: %w", collection, err)
	}
	if len(listResp.Items) == 0 {
		return "", fmt.Errorf("%s %q not found", collection, value)
	}
	return listResp.Items[0].ID, nil
}

func processRESTResult(resp *resty.Response, err error) ([]byte, bool) {

	var result []byte
	var error bool = false

	if err != nil {
		logger.Logger.Fatal(err)
		error = true
	}

	if resp.StatusCode() == 403 || resp.StatusCode() == 409 || resp.StatusCode() == 404 || resp.StatusCode() == 413 {
		// Try to parse as PocketBase error first, then fall back to simple error format
		err := prettyPrintPocketBaseError(resp.Body())
		if err != nil {
			// Not a PocketBase error, try simple error format
			prettyPrintError(resp.String())
		}
		error = true
	}

	if resp.StatusCode() == 400 {
		// Try to parse as PocketBase error first, then fall back to simple error format
		err := prettyPrintPocketBaseError(resp.Body())
		if err != nil {
			// Not a PocketBase error, try simple error format
			prettyPrintError(resp.String())
		}
		error = true
	}

	if resp.StatusCode() == 401 {
		logger.Logger.Errorf("Error with request. Check your API key with --verbose")
		prettyPrintError(resp.String())
		error = true
	}

	if resp.StatusCode() == 500 || resp.StatusCode() == 502 {
		logger.Logger.Error("Error from server!")
		err := prettyPrintPocketBaseError(resp.Body())
		if err != nil {
			prettyPrintError(resp.String())
		}
		error = true
	}

	if error {
		return nil, false
	}

	if resp.StatusCode() == 200 || resp.StatusCode() == 201 {
		result = resp.Body()
	}

	return result, true
}

func GenericGet(client *resty.Client, apiPath string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}

	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().Get(apiPath)

	s.Stop()

	return processRESTResult(resp, err)
}

func GenericJSONPost(client *resty.Client, apiPath string, data any) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Post(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

func GenericDelete(client *resty.Client, apiPath string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().Delete(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

func GenericDeleteWithBody(client *resty.Client, apiPath string, data any) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Delete(apiPath)

	s.Stop()

	return processRESTResult(resp, err)
}

// FileUpload performs a multipart upload (POST or PUT) with `data` attached as
// `fileField`/`filename` and any additional string `fields` set as form data.
// All file-uploading helpers in this package are thin wrappers around this one.
func FileUpload(client *resty.Client, method, apiPath, fileField, filename string, data []byte, fields map[string]string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()
	defer s.Stop()

	req := client.R().SetFileReader(fileField, filename, bytes.NewReader(data))
	if len(fields) > 0 {
		req = req.SetFormData(fields)
	}

	var (
		resp *resty.Response
		err  error
	)
	switch strings.ToUpper(method) {
	case "POST":
		resp, err = req.Post(apiPath)
	case "PUT":
		resp, err = req.Put(apiPath)
	case "PATCH":
		resp, err = req.Patch(apiPath)
	default:
		logger.Logger.Fatalf("FileUpload: unsupported method %q", method)
		return nil, false
	}
	return processRESTResult(resp, err)
}

func GenericPutFile(client *resty.Client, apiPath string, data []byte) ([]byte, bool) {
	return FileUpload(client, "PUT", apiPath, "file", "file", data, nil)
}

func PostFileAndForce(client *resty.Client, apiPath string, data []byte, filename string, force bool) ([]byte, bool) {
	return FileUpload(client, "PUT", apiPath, "file", filename, data, map[string]string{
		"force": fmt.Sprintf("%t", force),
	})
}

func PostFileAndForceAndGlobal(client *resty.Client, apiPath string, data []byte, filename string, force, ansibleGlobal bool) ([]byte, bool) {
	return FileUpload(client, "PUT", apiPath, "file", filename, data, map[string]string{
		"force":  fmt.Sprintf("%t", force),
		"global": fmt.Sprintf("%t", ansibleGlobal),
	})
}

func GenericJSONPut(client *resty.Client, apiPath string, data string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Put(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

func GenericJSONPatch(client *resty.Client, apiPath string, data string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Patch(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

// FileGet streams a GET response body to dst on success. The transfer goes to
// a sibling temp file first; on a non-2xx status the temp is deleted so the
// user's chosen path is never overwritten with an error body. Returns
// (errorBody, false) on failure, (nil, true) on success.
func FileGet(client *resty.Client, apiPath, dst string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, apiPrefix) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()
	defer s.Stop()

	dstDir := filepath.Dir(dst)
	if dstDir == "" {
		dstDir = "."
	}
	tmp, err := os.CreateTemp(dstDir, filepath.Base(dst)+".part-*")
	if err != nil {
		return processRESTResult(nil, err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	resp, err := client.R().SetOutput(tmpPath).Get(apiPath)
	if err != nil {
		return processRESTResult(resp, err)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		body, _ := os.ReadFile(tmpPath)
		return processRESTResult(resp, fmt.Errorf("%s", string(body)))
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return processRESTResult(resp, err)
	}
	return nil, true
}
